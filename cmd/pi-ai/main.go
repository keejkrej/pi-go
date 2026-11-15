// Ported from packages/ai/src/cli.ts (pi v1.5.2).

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	_ "github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const mainAuthFile = "auth.json"

func mainOAuthProviders() []*ai.Provider {
	all := providers.BuiltinProviders()
	out := make([]*ai.Provider, 0, len(all))
	for _, p := range all {
		if p != nil && p.Auth.OAuth != nil {
			out = append(out, p)
		}
	}
	return out
}

func mainFindProvider(list []*ai.Provider, id string) *ai.Provider {
	for _, p := range list {
		if p.Id == id {
			return p
		}
	}
	return nil
}

func mainRandomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func mainPrompt(reader *bufio.Reader, question string) (string, error) {
	fmt.Print(question)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func mainLoadAuth() *jsonx.Object {
	data, err := os.ReadFile(mainAuthFile)
	if err != nil {
		return jsonx.NewObject()
	}
	parsed, err := jsonx.Parse(data)
	if err != nil {
		return jsonx.NewObject()
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return jsonx.NewObject()
	}
	return obj
}

func mainSaveAuth(doc *jsonx.Object) error {
	// OAuth tokens: readable by the user only. Mode applies on create; chmod covers an older file
	// before new tokens are written to it.
	if _, err := os.Stat(mainAuthFile); err == nil {
		if err := os.Chmod(mainAuthFile, 0o600); err != nil {
			return err
		}
	}
	encoded, err := jsonx.StringifyIndent(doc, "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mainAuthFile, []byte(encoded), 0o600)
}

func mainPromptLabel(message string, placeholder *string) string {
	if placeholder != nil && *placeholder != "" {
		return fmt.Sprintf("%s (%s): ", message, *placeholder)
	}
	return fmt.Sprintf("%s: ", message)
}

func mainAnswerPrompt(reader *bufio.Reader, authPrompt ai.AuthPrompt) (string, error) {
	switch p := authPrompt.(type) {
	case *ai.AuthPromptSelect:
		fmt.Printf("\n%s\n", p.Message)
		for i, opt := range p.Options {
			fmt.Printf("  %d. %s\n", i+1, opt.Label)
		}
		raw, err := mainPrompt(reader, fmt.Sprintf("Enter number (1-%d): ", len(p.Options)))
		if err != nil {
			return "", err
		}
		choice, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || choice < 1 || choice > len(p.Options) {
			return "", fmt.Errorf("Invalid selection")
		}
		return p.Options[choice-1].Id, nil
	case *ai.AuthPromptText:
		return mainPrompt(reader, mainPromptLabel(p.Message, p.Placeholder))
	case *ai.AuthPromptSecret:
		return mainPrompt(reader, mainPromptLabel(p.Message, p.Placeholder))
	case *ai.AuthPromptManualCode:
		return mainPrompt(reader, mainPromptLabel(p.Message, p.Placeholder))
	default:
		return "", fmt.Errorf("Unsupported prompt type: %T", authPrompt)
	}
}

type mainCLIInteraction struct {
	reader *bufio.Reader
}

func (i *mainCLIInteraction) Prompt(_ context.Context, authPrompt ai.AuthPrompt) (string, error) {
	return mainAnswerPrompt(i.reader, authPrompt)
}

func (i *mainCLIInteraction) Notify(event ai.AuthEvent) {
	switch e := event.(type) {
	case *ai.AuthEventAuthUrl:
		fmt.Printf("\nOpen this URL in your browser:\n%s\n", e.Url)
		if e.Instructions != nil && *e.Instructions != "" {
			fmt.Println(*e.Instructions)
		}
	case *ai.AuthEventDeviceCode:
		fmt.Printf("\nOpen this URL in your browser:\n%s\n", e.VerificationUri)
		fmt.Printf("Enter code: %s\n", e.UserCode)
	case *ai.AuthEventInfo:
		fmt.Println(e.Message)
	case *ai.AuthEventProgress:
		fmt.Println(e.Message)
	}
}

func mainLogin(oauthProviders []*ai.Provider, reader *bufio.Reader, providerId string) error {
	provider := mainFindProvider(oauthProviders, providerId)
	if provider == nil || provider.Auth.OAuth == nil {
		return fmt.Errorf("Unknown provider: %s", providerId)
	}
	interaction := &mainCLIInteraction{reader: reader}
	// This dev CLI does not persist an installation ID; apps should reuse one across logins.
	credential, err := provider.Auth.OAuth.Login(
		context.Background(),
		interaction,
		&ai.LoginOptions{GetDeviceId: mainRandomUUID},
	)
	if err != nil {
		return err
	}
	doc := mainLoadAuth()
	doc.Set(providerId, credential)
	if err := mainSaveAuth(doc); err != nil {
		return err
	}
	fmt.Printf("\nCredentials saved to %s\n", mainAuthFile)
	return nil
}

func mainRun(args []string, stdin io.Reader) error {
	oauthProviders := mainOAuthProviders()
	var command string
	if len(args) > 0 {
		command = args[0]
	}
	if command == "" || command == "help" || command == "--help" || command == "-h" {
		lines := make([]string, len(oauthProviders))
		for i, p := range oauthProviders {
			lines[i] = fmt.Sprintf("  %-20s %s", p.Id, p.Name)
		}
		fmt.Printf(
			"Usage: npx @earendil-works/pi-ai <command> [provider]\n\nCommands:\n  login [provider]  Login to an OAuth provider\n  list              List available providers\n\nProviders:\n%s\n",
			strings.Join(lines, "\n"),
		)
		return nil
	}
	if command == "list" {
		for _, p := range oauthProviders {
			fmt.Printf("%-20s %s\n", p.Id, p.Name)
		}
		return nil
	}
	if command == "login" {
		reader := bufio.NewReader(stdin)
		var providerId string
		if len(args) > 1 {
			providerId = args[1]
		}
		if providerId == "" {
			for i, p := range oauthProviders {
				fmt.Printf("  %d. %s\n", i+1, p.Name)
			}
			raw, err := mainPrompt(reader, fmt.Sprintf("Enter number (1-%d): ", len(oauthProviders)))
			if err != nil {
				return err
			}
			choice, err := strconv.Atoi(strings.TrimSpace(raw))
			if err == nil && choice >= 1 && choice <= len(oauthProviders) {
				providerId = oauthProviders[choice-1].Id
			}
		}
		if providerId == "" || mainFindProvider(oauthProviders, providerId) == nil {
			return fmt.Errorf("Unknown provider: %s", providerId)
		}
		return mainLogin(oauthProviders, reader, providerId)
	}
	return fmt.Errorf("Unknown command: %s", command)
}

func main() {
	if err := mainRun(os.Args[1:], os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
		os.Exit(1)
	}
}
