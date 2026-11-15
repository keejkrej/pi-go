// Ported from packages/coding-agent/test/auth-command.test.ts (pi v1.5.2).

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tacStrPtr(s string) *string { return &s }

func TestSetupCliAndAuthCommandParsing(t *testing.T) {
	SetupCli()
	if got := t.Context(); got == nil {
		t.Fatal("unexpected nil context")
	}

	if got, err := ParseAuthCommand(nil); err != nil || got != nil {
		t.Fatalf("ParseAuthCommand(nil) = (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := ParseAuthCommand([]string{"--provider", "openai"}); err != nil || got != nil {
		t.Fatalf("ParseAuthCommand(non-auth) = (%v, %v), want (nil, nil)", got, err)
	}

	if !IsAuthCommandHelp([]string{"auth"}) || !IsAuthCommandHelp([]string{"auth", "help"}) || !IsAuthCommandHelp([]string{"auth", "login", "--help"}) || !IsAuthCommandHelp([]string{"auth", "-h"}) {
		t.Fatal("expected IsAuthCommandHelp to detect auth help invocations")
	}
	if IsAuthCommandHelp([]string{"auth", "check"}) || IsAuthCommandHelp([]string{"help"}) {
		t.Fatal("unexpected IsAuthCommandHelp true for non-help args")
	}

	var helpBuf bytes.Buffer
	acPrintAuthCommandHelpTo(&helpBuf)
	helpText := helpBuf.String()
	for _, want := range []string{"login <provider>", "logout <provider>", "print-api-key", "print-bearer-token", "check", "--min-expiry", "--no-refresh"} {
		if !strings.Contains(helpText, want) {
			t.Fatalf("help output missing %q:\n%s", want, helpText)
		}
	}

	checkCmd, err := ParseAuthCommand([]string{"auth", "check", "--provider", "anthropic", "--json", "--credentials", "--no-refresh"})
	if err != nil {
		t.Fatalf("ParseAuthCommand(check): %v", err)
	}
	if checkCmd.Kind != AuthCommandKindCheck || !checkCmd.Json || !checkCmd.Credentials || !checkCmd.NoRefresh {
		t.Fatalf("unexpected checkCmd: %+v", checkCmd)
	}
	if len(checkCmd.Args) != 2 || checkCmd.Args[0] != "--provider" || checkCmd.Args[1] != "anthropic" {
		t.Fatalf("unexpected checkCmd.Args: %v", checkCmd.Args)
	}

	bearerCmd, err := ParseAuthCommand([]string{"auth", "print-bearer-token", "--provider", "openai-codex", "--min-expiry", "45m"})
	if err != nil {
		t.Fatalf("ParseAuthCommand(print-bearer-token): %v", err)
	}
	if bearerCmd.Kind != AuthCommandKindBearerToken || bearerCmd.MinExpiryMs == nil || *bearerCmd.MinExpiryMs != 45*60_000 {
		t.Fatalf("unexpected bearerCmd: %+v", bearerCmd)
	}

	for _, bad := range [][]string{
		{"auth", "bogus"},
		{"auth", "check", "--min-expiry", "10m"},
		{"auth", "print-api-key", "--json"},
		{"auth", "print-bearer-token", "--no-refresh"},
		{"auth", "print-bearer-token", "--min-expiry", "soon"},
		{"auth", "print-bearer-token", "--min-expiry"},
	} {
		if _, err := ParseAuthCommand(bad); err == nil {
			t.Fatalf("expected error parsing %v", bad)
		}
	}
}

func TestValidateAuthCommandArgsAndGetAuthCredential(t *testing.T) {
	prov, model, err := ValidateAuthCommandArgs(core.Args{Provider: tacStrPtr(" openai ")}, AuthCommandKindApiKey)
	if err != nil || prov == nil || *prov != "openai" || model != nil {
		t.Fatalf("ValidateAuthCommandArgs valid provider = (%v, %v, %v)", prov, model, err)
	}

	if _, _, err := ValidateAuthCommandArgs(core.Args{}, AuthCommandKindCheck); err == nil || !strings.Contains(err.Error(), "Auth checks require") {
		t.Fatalf("expected check missing-target error, got %v", err)
	}
	if _, _, err := ValidateAuthCommandArgs(core.Args{}, AuthCommandKindApiKey); err == nil || !strings.Contains(err.Error(), "Credential printing requires") {
		t.Fatalf("expected credential missing-target error, got %v", err)
	}
	if _, _, err := ValidateAuthCommandArgs(core.Args{Provider: tacStrPtr("openai"), ApiKey: tacStrPtr("sk-1")}, AuthCommandKindApiKey); err == nil {
		t.Fatal("expected error when --api-key is passed")
	}
	unknown := omap.NewMap[string, core.UnknownFlagValue]()
	unknown.Set("bogus", core.UnknownFlagValue{Bool: true})
	if _, _, err := ValidateAuthCommandArgs(core.Args{Provider: tacStrPtr("openai"), UnknownFlags: unknown}, AuthCommandKindCheck); err == nil || !strings.Contains(err.Error(), "--bogus") {
		t.Fatalf("expected unknown flag error, got %v", err)
	}

	if GetAuthCredential(nil) != nil {
		t.Fatal("expected nil for nil AuthResult")
	}
	apiKeyCred := GetAuthCredential(&ai.AuthResult{Auth: ai.ModelAuth{ApiKey: tacStrPtr("sk-direct")}})
	if apiKeyCred == nil || *apiKeyCred != "sk-direct" {
		t.Fatalf("GetAuthCredential ApiKey = %v, want sk-direct", apiKeyCred)
	}
	headers := omap.NewMap[string, *string]()
	headers.Set("Authorization", tacStrPtr("Bearer tok-123"))
	bearerCred := GetAuthCredential(&ai.AuthResult{Auth: ai.ModelAuth{Headers: headers}})
	if bearerCred == nil || *bearerCred != "tok-123" {
		t.Fatalf("GetAuthCredential Bearer = %v, want tok-123", bearerCred)
	}
	emptyHeaders := omap.NewMap[string, *string]()
	emptyHeaders.Set("X-Api-Key", tacStrPtr("not-bearer"))
	if got := GetAuthCredential(&ai.AuthResult{Auth: ai.ModelAuth{Headers: emptyHeaders}}); got != nil {
		t.Fatalf("expected nil for non-Bearer header, got %q", *got)
	}
}
