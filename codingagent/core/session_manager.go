// Ported from packages/coding-agent/src/core/session-manager.ts (pi v1.5.2).

package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// CurrentSessionVersion is the current JSONL session file format version.
const CurrentSessionVersion = 3

const (
	smReadBufferSize                = 1024 * 1024
	smHeaderReadBufferSize          = 4096
	smMaxHeaderScanBytes            = 1024 * 1024
	smMaxConcurrentSessionInfoLoads = 10
	smMaxConcurrentDiscoveryLoads   = 64
	smCurrentListPublishInterval    = 10
	smAllListPublishInterval        = 100
)

var smValidSessionIdRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
var smLineBreakRe = regexp.MustCompile(`[\r\n]+`)
var smFilenameSafeTimeRe = regexp.MustCompile(`[:.]`)
var smLeadingSepRe = regexp.MustCompile(`^[/\\]`)
var smPathSepRe = regexp.MustCompile(`[/\\:]`)

type smHeaderScanLimitError struct {
	filePath string
}

func (e *smHeaderScanLimitError) Error() string {
	return fmt.Sprintf("Session header exceeds %d-byte scan limit: %s", smMaxHeaderScanBytes, e.filePath)
}

// FileEntry is any line stored in a session JSONL file (SessionHeader or SessionEntry).
type FileEntry interface {
	isFileEntry()
	GetType() string
	GetTimestamp() string
	rawObject() *jsonx.Object
	marshalJsonLine() (string, error)
}

// SessionEntry is any non-header node in the session entry tree.
type SessionEntry interface {
	FileEntry
	isSessionEntry()
	GetId() string
	SetId(id string)
	GetParentId() *string
	SetParentId(parentId *string)
	cloneWithParentId(parentId *string) SessionEntry
}

// SessionHeader is the first line of a session JSONL file.
type SessionHeader struct {
	Type          string        `json:"type"`
	Version       *int          `json:"version,omitzero"`
	Id            string        `json:"id"`
	Timestamp     string        `json:"timestamp"`
	Cwd           string        `json:"cwd"`
	ParentSession *string       `json:"parentSession,omitzero"`
	Raw           *jsonx.Object `json:"-"`
}

func (*SessionHeader) isFileEntry()               {}
func (h *SessionHeader) GetType() string          { return h.Type }
func (h *SessionHeader) GetTimestamp() string     { return h.Timestamp }
func (h *SessionHeader) rawObject() *jsonx.Object { return h.Raw }
func (h *SessionHeader) marshalJsonLine() (string, error) {
	if h.Raw != nil {
		obj := h.Raw.Clone()
		obj.Set("type", h.Type)
		if h.Version != nil {
			obj.Set("version", *h.Version)
		} else {
			obj.Delete("version")
		}
		obj.Set("id", h.Id)
		obj.Set("timestamp", h.Timestamp)
		obj.Set("cwd", h.Cwd)
		if h.ParentSession != nil {
			obj.Set("parentSession", *h.ParentSession)
		}
		return jsonx.Stringify(obj)
	}
	return jsonx.Stringify(h)
}

// NewSessionOptions customizes ID and lineage metadata when creating a new session.
type NewSessionOptions struct {
	Id            *string `json:"id,omitzero"`
	ParentSession *string `json:"parentSession,omitzero"`
}

// SessionEntryBase holds fields common to every SessionEntry variant.
type SessionEntryBase struct {
	Type      string        `json:"type"`
	Id        string        `json:"id"`
	ParentId  *string       `json:"parentId"`
	Timestamp string        `json:"timestamp"`
	Raw       *jsonx.Object `json:"-"`
}

func (b *SessionEntryBase) isFileEntry()             {}
func (b *SessionEntryBase) isSessionEntry()          {}
func (b *SessionEntryBase) GetType() string          { return b.Type }
func (b *SessionEntryBase) GetTimestamp() string     { return b.Timestamp }
func (b *SessionEntryBase) GetId() string            { return b.Id }
func (b *SessionEntryBase) GetParentId() *string     { return smCopyStrPtr(b.ParentId) }
func (b *SessionEntryBase) rawObject() *jsonx.Object { return b.Raw }

func (b *SessionEntryBase) SetId(id string) {
	b.Id = id
	if b.Raw != nil {
		b.Raw.Set("id", id)
	}
}

func (b *SessionEntryBase) SetParentId(parentId *string) {
	b.ParentId = smCopyStrPtr(parentId)
	if b.Raw != nil {
		if b.ParentId == nil {
			b.Raw.Set("parentId", nil)
		} else {
			b.Raw.Set("parentId", *b.ParentId)
		}
	}
}

func smCloneBase(b SessionEntryBase, parentId *string) SessionEntryBase {
	cp := b
	cp.ParentId = smCopyStrPtr(parentId)
	if b.Raw != nil {
		cp.Raw = b.Raw.Clone()
		if cp.ParentId == nil {
			cp.Raw.Set("parentId", nil)
		} else {
			cp.Raw.Set("parentId", *cp.ParentId)
		}
	}
	return cp
}

func smMarshalWithRaw(raw *jsonx.Object, entry any, updates ...func(obj *jsonx.Object)) (string, error) {
	if raw != nil {
		obj := raw.Clone()
		for _, fn := range updates {
			fn(obj)
		}
		return jsonx.Stringify(obj)
	}
	return jsonx.Stringify(entry)
}

// SessionMessageEntry stores one conversation or extension message on the session tree.
type SessionMessageEntry struct {
	SessionEntryBase
	Message agent.AgentMessage `json:"message"`
}

func (e *SessionMessageEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *SessionMessageEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// ThinkingLevelChangeEntry records a user or system change to reasoning effort.
type ThinkingLevelChangeEntry struct {
	SessionEntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

func (e *ThinkingLevelChangeEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *ThinkingLevelChangeEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// ModelChangeEntry records switching the active provider and model ID.
type ModelChangeEntry struct {
	SessionEntryBase
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

func (e *ModelChangeEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *ModelChangeEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// UsageEntry records non-context token/cost usage such as cache warming.
type UsageEntry struct {
	SessionEntryBase
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Usage    ai.Usage `json:"usage"`
	Note     *string  `json:"note,omitzero"`
}

func (e *UsageEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *UsageEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// CompactionEntry summarizes older session entries up to FirstKeptEntryId.
type CompactionEntry struct {
	SessionEntryBase
	Summary             string            `json:"summary"`
	FirstKeptEntryId    string            `json:"firstKeptEntryId"`
	FirstKeptEntryIndex *int              `json:"firstKeptEntryIndex,omitzero"`
	TokensBefore        int               `json:"tokensBefore"`
	Details             any               `json:"details,omitzero"`
	Usage               *ai.Usage         `json:"usage,omitzero"`
	FromHook            *bool             `json:"fromHook,omitzero"`
	SystemMessage       *ai.SystemMessage `json:"systemMessage,omitzero"`
}

func (e *CompactionEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *CompactionEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e, func(obj *jsonx.Object) {
		obj.Set("firstKeptEntryId", e.FirstKeptEntryId)
		if e.FirstKeptEntryIndex == nil {
			obj.Delete("firstKeptEntryIndex")
		}
	})
}

// BranchSummaryEntry captures a summary of an abandoned branch when switching branches.
type BranchSummaryEntry struct {
	SessionEntryBase
	FromId   string    `json:"fromId"`
	Summary  string    `json:"summary"`
	Details  any       `json:"details,omitzero"`
	Usage    *ai.Usage `json:"usage,omitzero"`
	FromHook *bool     `json:"fromHook,omitzero"`
}

func (e *BranchSummaryEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *BranchSummaryEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// CustomEntry stores extension-specific state outside LLM context.
type CustomEntry struct {
	SessionEntryBase
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitzero"`
}

func (e *CustomEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *CustomEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// CustomMessageEntry injects extension-authored message content into LLM context.
type CustomMessageEntry struct {
	SessionEntryBase
	CustomType string                `json:"customType"`
	Content    ai.UserMessageContent `json:"content"`
	Details    any                   `json:"details,omitzero"`
	Display    bool                  `json:"display"`
}

func (e *CustomMessageEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *CustomMessageEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// ContextEditReplacement specifies new message content in a ContextEditEntry.
type ContextEditReplacement struct {
	Content ai.UserMessageContent `json:"content"`
}

// ContextEditEntry records an append-only omission or content replacement for targetId.
type ContextEditEntry struct {
	SessionEntryBase
	TargetId    string                  `json:"targetId"`
	Replacement *ContextEditReplacement `json:"replacement"`
}

func (e *ContextEditEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *ContextEditEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// LabelEntry attaches or clears a user-defined bookmark label on TargetId.
type LabelEntry struct {
	SessionEntryBase
	TargetId string  `json:"targetId"`
	Label    *string `json:"label,omitzero"`
}

func (e *LabelEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *LabelEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// SessionInfoEntry updates user-visible session metadata such as display name.
type SessionInfoEntry struct {
	SessionEntryBase
	Name *string `json:"name,omitzero"`
}

func (e *SessionInfoEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *SessionInfoEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// UnknownSessionEntry preserves unrecognized entry types loaded from disk.
type UnknownSessionEntry struct {
	SessionEntryBase
}

func (e *UnknownSessionEntry) cloneWithParentId(parentId *string) SessionEntry {
	cp := *e
	cp.SessionEntryBase = smCloneBase(e.SessionEntryBase, parentId)
	return &cp
}

func (e *UnknownSessionEntry) marshalJsonLine() (string, error) {
	return smMarshalWithRaw(e.Raw, e)
}

// SessionTreeNode represents one entry and its sorted child subtrees returned by GetTree.
type SessionTreeNode struct {
	Entry          SessionEntry      `json:"entry"`
	Children       []SessionTreeNode `json:"children"`
	Label          *string           `json:"label,omitzero"`
	LabelTimestamp *string           `json:"labelTimestamp,omitzero"`
}

// ProjectedSessionEntry pairs a raw entry with its projected model-visible messages.
type ProjectedSessionEntry struct {
	SourceEntry SessionEntry         `json:"sourceEntry"`
	Messages    []agent.AgentMessage `json:"messages"`
}

// SessionModelRef identifies the active provider and model ID resolved along a branch.
type SessionModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// SessionProjection holds provenance-preserving projected entries, flattened messages,
// active thinking level, and active model reference.
type SessionProjection struct {
	Entries       []ProjectedSessionEntry `json:"entries"`
	Messages      []agent.AgentMessage    `json:"messages"`
	ThinkingLevel string                  `json:"thinkingLevel"`
	Model         *SessionModelRef        `json:"model"`
}

// SessionContext holds the flattened model messages, active thinking level, and active model.
type SessionContext struct {
	Messages      []agent.AgentMessage `json:"messages"`
	ThinkingLevel string               `json:"thinkingLevel"`
	Model         *SessionModelRef     `json:"model"`
}

// SessionInfo summarizes a persisted session file for session picker listings.
type SessionInfo struct {
	Path              string    `json:"path"`
	Id                string    `json:"id"`
	Cwd               string    `json:"cwd"`
	Name              *string   `json:"name,omitzero"`
	ParentSessionPath *string   `json:"parentSessionPath,omitzero"`
	Created           time.Time `json:"created"`
	Modified          time.Time `json:"modified"`
	MessageCount      int       `json:"messageCount"`
	FirstMessage      string    `json:"firstMessage"`
	AllMessagesText   string    `json:"allMessagesText"`
}

// SessionListProgress receives periodic progress notifications while listing sessions.
type SessionListProgress func(loaded int, total int, partialSessions []SessionInfo)

// ReadonlySessionManager exposes the read-only inspection subset of SessionManager.
type ReadonlySessionManager interface {
	GetCwd() string
	GetSessionDir() string
	GetSessionId() string
	GetSessionFile() *string
	GetLeafId() *string
	GetLeafEntry() SessionEntry
	GetEntry(id string) SessionEntry
	GetLabel(id string) *string
	GetBranch(fromId ...string) []SessionEntry
	BuildContextEntries() []SessionEntry
	BuildSessionProjection() SessionProjection
	GetHeader() *SessionHeader
	GetEntries() []SessionEntry
	GetTree() []SessionTreeNode
	GetSessionName() *string
}

// SessionOpenOptions configures SessionManagerOpen.
type SessionOpenOptions struct {
	SessionDir  string
	CwdOverride *string
}

// AppendCompactionOptions provides optional fields to SessionManager.AppendCompaction.
type AppendCompactionOptions struct {
	Details  any
	FromHook *bool
	Usage    *ai.Usage
}

// BranchWithSummaryOptions provides optional fields to SessionManager.BranchWithSummary.
type BranchWithSummaryOptions struct {
	Details  any
	FromHook *bool
	Usage    *ai.Usage
}

func smCopyStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func smCopyBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func smResolvePath(p string) string {
	resolved, err := utils.ResolvePath(p, "", nil)
	if err != nil {
		return p
	}
	return resolved
}

func smNormalizePath(p string) string {
	if p == "" {
		return ""
	}
	normalized, err := utils.NormalizePath(p, nil)
	if err != nil {
		return p
	}
	return normalized
}

func smNowISO() string {
	return jsonx.FormatISOTime(time.Now().UTC())
}

func smParseISOTime(ts string) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func smParseTimeMs(ts string) (int64, bool) {
	t, ok := smParseISOTime(ts)
	if !ok {
		return 0, false
	}
	return t.UnixMilli(), true
}

func smCreateSessionId() string {
	return ai.Uuidv7()
}

// AssertValidSessionId validates a caller-supplied session identifier against pi filename rules.
func AssertValidSessionId(id string) error {
	if !smValidSessionIdRe.MatchString(id) {
		return errors.New("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
	}
	return nil
}

func smRandomShortId() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
}

func smGenerateId(used map[string]struct{}) string {
	for i := 0; i < 100; i++ {
		id := smRandomShortId()
		if _, exists := used[id]; !exists {
			return id
		}
	}
	return ai.Uuidv7()
}

func smDecodeSessionAgentMessage(v any) (agent.AgentMessage, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return agent.DecodeAgentMessage(v)
	}
	roleVal, _ := obj.Get("role")
	role, _ := roleVal.(string)
	switch role {
	case "system", "user", "assistant", "toolResult":
		return ai.DecodeMessage(obj)
	case "bashExecution":
		var out BashExecutionMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "custom":
		var out CustomMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "branchSummary":
		var out BranchSummaryMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "compactionSummary":
		var out CompactionSummaryMessage
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return agent.DecodeAgentMessage(obj)
	}
}

func smDecodeFileEntry(parsed any) FileEntry {
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil
	}
	typ, _ := obj.GetString("type")
	switch typ {
	case "session":
		var out SessionHeader
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "message":
		var base SessionEntryBase
		if err := jsonx.Decode(obj, &base); err != nil {
			return nil
		}
		base.Raw = obj
		msgVal, _ := obj.Get("message")
		msg, err := smDecodeSessionAgentMessage(msgVal)
		if err != nil {
			msg = &agent.UnknownAgentMessage{Raw: nil}
			if msgObj, ok := msgVal.(*jsonx.Object); ok {
				msg = &agent.UnknownAgentMessage{Raw: msgObj}
			}
		}
		return &SessionMessageEntry{
			SessionEntryBase: base,
			Message:          msg,
		}
	case "thinking_level_change":
		var out ThinkingLevelChangeEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "model_change":
		var out ModelChangeEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "usage":
		var out UsageEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "compaction":
		var out CompactionEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "branch_summary":
		var out BranchSummaryEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "custom":
		var out CustomEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "custom_message":
		var out CustomMessageEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "context_edit":
		var out ContextEditEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "label":
		var out LabelEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	case "session_info":
		var out SessionInfoEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	default:
		var out UnknownSessionEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil
		}
		out.Raw = obj
		return &out
	}
}

func smParseEntryLine(line string) FileEntry {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	parsed, err := jsonx.ParseString(line)
	if err != nil {
		return nil
	}
	return smDecodeFileEntry(parsed)
}

func smMigrateV1ToV2(entries []FileEntry) {
	ids := make(map[string]struct{})
	var prevId *string

	for _, entry := range entries {
		if h, ok := entry.(*SessionHeader); ok {
			v := 2
			h.Version = &v
			if h.Raw != nil {
				h.Raw.Set("version", 2)
			}
			continue
		}
		se, ok := entry.(SessionEntry)
		if !ok {
			continue
		}
		id := smGenerateId(ids)
		ids[id] = struct{}{}
		se.SetId(id)
		se.SetParentId(prevId)
		prevId = &id

		if comp, ok := se.(*CompactionEntry); ok && comp.FirstKeptEntryIndex != nil {
			idx := *comp.FirstKeptEntryIndex
			if idx >= 0 && idx < len(entries) {
				if target, ok := entries[idx].(SessionEntry); ok {
					comp.FirstKeptEntryId = target.GetId()
					if comp.Raw != nil {
						comp.Raw.Set("firstKeptEntryId", comp.FirstKeptEntryId)
					}
				}
			}
			comp.FirstKeptEntryIndex = nil
			if comp.Raw != nil {
				comp.Raw.Delete("firstKeptEntryIndex")
			}
		}
	}
}

func smMigrateV2ToV3(entries []FileEntry) {
	for _, entry := range entries {
		if h, ok := entry.(*SessionHeader); ok {
			v := 3
			h.Version = &v
			if h.Raw != nil {
				h.Raw.Set("version", 3)
			}
			continue
		}
		if msgEntry, ok := entry.(*SessionMessageEntry); ok && msgEntry.Message != nil && msgEntry.Message.GetRole() == "hookMessage" {
			if msgEntry.Raw != nil {
				if msgVal, exists := msgEntry.Raw.Get("message"); exists {
					if msgObj, isObj := msgVal.(*jsonx.Object); isObj && msgObj != nil {
						msgObj.Set("role", "custom")
						if decoded, err := smDecodeSessionAgentMessage(msgObj); err == nil {
							msgEntry.Message = decoded
						}
					}
				}
			}
			if u, ok := msgEntry.Message.(*agent.UnknownAgentMessage); ok && u != nil && u.Raw != nil {
				u.Raw.Set("role", "custom")
				if decoded, err := smDecodeSessionAgentMessage(u.Raw); err == nil {
					msgEntry.Message = decoded
				}
			}
		}
	}
}

func smMigrateToCurrentVersion(entries []FileEntry) bool {
	var header *SessionHeader
	for _, e := range entries {
		if h, ok := e.(*SessionHeader); ok {
			header = h
			break
		}
	}
	version := 1
	if header != nil && header.Version != nil {
		version = *header.Version
	}
	if version >= CurrentSessionVersion {
		return false
	}
	if version < 2 {
		smMigrateV1ToV2(entries)
	}
	if version < 3 {
		smMigrateV2ToV3(entries)
	}
	return true
}

// MigrateSessionEntries updates parsed session entries in place to CurrentSessionVersion.
func MigrateSessionEntries(entries []FileEntry) {
	smMigrateToCurrentVersion(entries)
}

// ParseSessionEntries parses JSONL session lines into FileEntry values, skipping blank and malformed lines.
func ParseSessionEntries(content string) []FileEntry {
	var entries []FileEntry
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for _, line := range lines {
		if entry := smParseEntryLine(line); entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// GetLatestCompactionEntry returns the most recent CompactionEntry in entries, or nil.
func GetLatestCompactionEntry(entries []SessionEntry) *CompactionEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if comp, ok := entries[i].(*CompactionEntry); ok {
			return comp
		}
	}
	return nil
}

func smBuildEntryIndex(entries []SessionEntry, byId ...map[string]SessionEntry) map[string]SessionEntry {
	if len(byId) > 0 && byId[0] != nil {
		return byId[0]
	}
	index := make(map[string]SessionEntry, len(entries))
	for _, entry := range entries {
		index[entry.GetId()] = entry
	}
	return index
}

func smBuildSessionPath(entries []SessionEntry, leafId *string, isExplicitNull bool, byId ...map[string]SessionEntry) []SessionEntry {
	index := smBuildEntryIndex(entries, byId...)
	if isExplicitNull {
		return []SessionEntry{}
	}
	var leaf SessionEntry
	if leafId != nil && *leafId != "" {
		leaf = index[*leafId]
	}
	if leaf == nil && len(entries) > 0 {
		leaf = entries[len(entries)-1]
	}
	if leaf == nil {
		return []SessionEntry{}
	}

	var path []SessionEntry
	for current := leaf; current != nil; {
		path = append(path, current)
		pid := current.GetParentId()
		if pid == nil || *pid == "" {
			break
		}
		current = index[*pid]
	}
	slices.Reverse(path)
	return path
}

func smGetSessionContextSettings(path []SessionEntry) (string, *SessionModelRef) {
	thinkingLevel := "off"
	var model *SessionModelRef
	for _, entry := range path {
		switch e := entry.(type) {
		case *ThinkingLevelChangeEntry:
			thinkingLevel = e.ThinkingLevel
		case *ModelChangeEntry:
			model = &SessionModelRef{Provider: e.Provider, ModelId: e.ModelId}
		case *SessionMessageEntry:
			if am, ok := e.Message.(*ai.AssistantMessage); ok && am != nil {
				model = &SessionModelRef{Provider: string(am.Provider), ModelId: am.Model}
			}
		}
	}
	return thinkingLevel, model
}

func smCopyAssistantMessage(src *ai.AssistantMessage, content ai.ContentList) *ai.AssistantMessage {
	if src == nil {
		return nil
	}
	return &ai.AssistantMessage{
		Role:                  src.Role,
		Content:               content,
		Api:                   src.Api,
		Provider:              src.Provider,
		Model:                 src.Model,
		ResponseModel:         src.ResponseModel,
		ResponseId:            src.ResponseId,
		ProviderThinkingLevel: src.ProviderThinkingLevel,
		ThinkingLevel:         src.ThinkingLevel,
		Diagnostics:           src.Diagnostics,
		Usage:                 src.Usage,
		StopReason:            src.StopReason,
		Deferred:              src.Deferred,
		ErrorMessage:          src.ErrorMessage,
		RawStopReason:         src.RawStopReason,
		EndTurn:               src.EndTurn,
		Timestamp:             src.Timestamp,
		DurationMs:            src.DurationMs,
	}
}

// SessionEntryToContextMessages projects one SessionEntry into LLM/runtime messages.
func SessionEntryToContextMessages(entry SessionEntry) []agent.AgentMessage {
	switch e := entry.(type) {
	case *SessionMessageEntry:
		msg := e.Message
		if msg == nil {
			return nil
		}
		switch m := msg.(type) {
		case *ai.SystemMessage:
			if m.Content.Text == nil && m.Content.Blocks == nil {
				cp := *m
				empty := ""
				cp.Content = ai.SystemMessageContent{Text: &empty}
				return []agent.AgentMessage{&cp}
			}
		case *ai.UserMessage:
			if m.Content.Text == nil && m.Content.Blocks == nil {
				cp := *m
				cp.Content = ai.UserMessageContent{Blocks: ai.ContentList{}}
				return []agent.AgentMessage{&cp}
			}
		case *ai.AssistantMessage:
			if m.Content == nil {
				return []agent.AgentMessage{smCopyAssistantMessage(m, ai.ContentList{})}
			}
		case *ai.ToolResultMessage:
			if m.Content == nil {
				cp := *m
				cp.Content = ai.ContentList{}
				return []agent.AgentMessage{&cp}
			}
		}
		return []agent.AgentMessage{msg}
	case *CustomMessageEntry:
		content := e.Content
		if content.Text == nil && content.Blocks == nil {
			content = ai.UserMessageContent{Blocks: ai.ContentList{}}
		}
		return []agent.AgentMessage{
			CreateCustomMessage(e.CustomType, content, e.Display, e.Details, e.Timestamp),
		}
	case *BranchSummaryEntry:
		if e.Summary != "" {
			return []agent.AgentMessage{
				CreateBranchSummaryMessage(e.Summary, e.FromId, e.Timestamp),
			}
		}
	case *CompactionEntry:
		summary := CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp)
		if e.SystemMessage != nil {
			return []agent.AgentMessage{e.SystemMessage, summary}
		}
		return []agent.AgentMessage{summary}
	}
	return nil
}

// BuildContextEntries resolves the active, compaction-aware entry slice along leafId (or latest entry).
func BuildContextEntries(entries []SessionEntry, leafId *string, byId ...map[string]SessionEntry) []SessionEntry {
	path := smBuildSessionPath(entries, leafId, false, byId...)
	return smBuildContextEntriesFromPath(path)
}

func smBuildContextEntriesFromPath(path []SessionEntry) []SessionEntry {
	var compaction *CompactionEntry
	compactionIdx := -1
	for i, entry := range path {
		if comp, ok := entry.(*CompactionEntry); ok {
			compaction = comp
			compactionIdx = i
		}
	}
	if compaction == nil || compactionIdx < 0 {
		return path
	}

	contextEntries := []SessionEntry{compaction}
	foundFirstKept := false
	for i := 0; i < compactionIdx; i++ {
		entry := path[i]
		if entry.GetId() == compaction.FirstKeptEntryId {
			foundFirstKept = true
		}
		if foundFirstKept {
			if msgEntry, ok := entry.(*SessionMessageEntry); ok && msgEntry.Message != nil && msgEntry.Message.GetRole() == "system" {
				continue
			}
			contextEntries = append(contextEntries, entry)
		}
	}
	contextEntries = append(contextEntries, path[compactionIdx+1:]...)
	return contextEntries
}

// ProjectContextEntry applies edit (omission or replacement) to one entry's context messages.
func ProjectContextEntry(entry SessionEntry, edit *ContextEditEntry) []agent.AgentMessage {
	messages := SessionEntryToContextMessages(entry)
	if edit == nil {
		return messages
	}
	if edit.Replacement == nil {
		return nil
	}
	repl := edit.Replacement
	out := make([]agent.AgentMessage, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case *ai.UserMessage:
			cp := *m
			cp.Content = repl.Content
			out[i] = &cp
		case *ai.AssistantMessage:
			content := repl.Content.Blocks
			if repl.Content.Text != nil {
				content = ai.ContentList{&ai.TextContent{Type: "text", Text: *repl.Content.Text}}
			}
			out[i] = smCopyAssistantMessage(m, content)
		case *ai.ToolResultMessage:
			cp := *m
			if repl.Content.Text != nil {
				cp.Content = ai.ContentList{&ai.TextContent{Type: "text", Text: *repl.Content.Text}}
			} else {
				cp.Content = repl.Content.Blocks
			}
			out[i] = &cp
		case *CustomMessage:
			cp := *m
			cp.Content = repl.Content
			out[i] = &cp
		default:
			out[i] = msg
		}
	}
	return out
}

// BuildSessionProjection builds the compaction-aware, context-edit-applied session projection.
func BuildSessionProjection(entries []SessionEntry, leafId *string, byId ...map[string]SessionEntry) SessionProjection {
	return smBuildSessionProjection(entries, leafId, false, byId...)
}

func smBuildSessionProjection(entries []SessionEntry, leafId *string, explicitNull bool, byId ...map[string]SessionEntry) SessionProjection {
	path := smBuildSessionPath(entries, leafId, explicitNull, byId...)
	thinkingLevel, model := smGetSessionContextSettings(path)
	contextEntries := smBuildContextEntriesFromPath(path)

	edits := make(map[string]*ContextEditEntry)
	for _, entry := range contextEntries {
		if edit, ok := entry.(*ContextEditEntry); ok {
			edits[edit.TargetId] = edit
		}
	}

	projectedEntries := make([]ProjectedSessionEntry, len(contextEntries))
	var messages []agent.AgentMessage
	for i, sourceEntry := range contextEntries {
		var entryMsgs []agent.AgentMessage
		if _, isComp := sourceEntry.(*CompactionEntry); isComp && i > 0 {
			entryMsgs = nil
		} else {
			entryMsgs = ProjectContextEntry(sourceEntry, edits[sourceEntry.GetId()])
		}
		projectedEntries[i] = ProjectedSessionEntry{
			SourceEntry: sourceEntry,
			Messages:    entryMsgs,
		}
		messages = append(messages, entryMsgs...)
	}
	return SessionProjection{
		Entries:       projectedEntries,
		Messages:      messages,
		ThinkingLevel: thinkingLevel,
		Model:         model,
	}
}

// BuildSessionContext returns the resolved messages, thinking level, and model for leafId.
func BuildSessionContext(entries []SessionEntry, leafId *string, byId ...map[string]SessionEntry) SessionContext {
	proj := BuildSessionProjection(entries, leafId, byId...)
	return SessionContext{
		Messages:      proj.Messages,
		ThinkingLevel: proj.ThinkingLevel,
		Model:         proj.Model,
	}
}

func smGetDefaultSessionDirPath(cwd string, agentDir ...string) string {
	dir := utils.GetAgentDir()
	if len(agentDir) > 0 && agentDir[0] != "" {
		dir = agentDir[0]
	}
	resolvedCwd := smResolvePath(cwd)
	resolvedAgentDir := smResolvePath(dir)
	trimmed := smLeadingSepRe.ReplaceAllString(resolvedCwd, "")
	safePath := "--" + smPathSepRe.ReplaceAllString(trimmed, "-") + "--"
	return filepath.Join(resolvedAgentDir, "sessions", safePath)
}

// GetDefaultSessionDir returns and creates `~/.pi/agent/sessions/<encoded-cwd>` (or under agentDir).
func GetDefaultSessionDir(cwd string, agentDir ...string) string {
	sessionDir := smGetDefaultSessionDirPath(cwd, agentDir...)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		_ = os.MkdirAll(sessionDir, 0o755)
	}
	return sessionDir
}

// LoadEntriesFromFile loads and validates JSONL session entries, repairing a missing trailing newline.
func LoadEntriesFromFile(filePath string) []FileEntry {
	resolved := smNormalizePath(filePath)
	f, err := os.Open(resolved)
	if err != nil {
		return []FileEntry{}
	}

	var entries []FileEntry
	hadUnterminatedLastLine := false

	reader := bufio.NewReaderSize(f, smReadBufferSize)
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if strings.HasSuffix(line, "\n") {
				if entry := smParseEntryLine(line[:len(line)-1]); entry != nil {
					entries = append(entries, entry)
				}
			} else {
				hadUnterminatedLastLine = true
				if entry := smParseEntryLine(line); entry != nil {
					entries = append(entries, entry)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	_ = f.Close()

	if len(entries) == 0 {
		return entries
	}
	header, ok := entries[0].(*SessionHeader)
	if !ok || header.Type != "session" {
		return []FileEntry{}
	}
	if header.Raw != nil {
		if _, hasStrId := header.Raw.GetString("id"); !hasStrId {
			return []FileEntry{}
		}
	}

	if hadUnterminatedLastLine {
		if af, err := os.OpenFile(resolved, os.O_WRONLY|os.O_APPEND, 0o666); err == nil {
			_, _ = af.WriteString("\n")
			_ = af.Close()
		}
	}
	return entries
}

func smParseHeaderCandidate(line string) (header *SessionHeader, found bool, malformedSkip bool) {
	if strings.TrimSpace(line) == "" {
		return nil, false, true
	}
	entry := smParseEntryLine(line)
	if entry == nil {
		return nil, false, true
	}
	h, ok := entry.(*SessionHeader)
	if !ok || h.Type != "session" {
		return nil, true, false
	}
	if h.Raw != nil {
		if _, hasStrId := h.Raw.GetString("id"); !hasStrId {
			return nil, true, false
		}
	}
	return h, true, false
}

func smReadSessionHeader(filePath string) (*SessionHeader, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, smHeaderReadBufferSize)
	var lineBuf []byte
	scannedBytes := 0

	for scannedBytes < smMaxHeaderScanBytes {
		toRead := len(buf)
		if rem := smMaxHeaderScanBytes - scannedBytes; rem < toRead {
			toRead = rem
		}
		n, readErr := f.Read(buf[:toRead])
		if n > 0 {
			scannedBytes += n
			chunk := buf[:n]
			start := 0
			for start < len(chunk) {
				idx := -1
				for i := start; i < len(chunk); i++ {
					if chunk[i] == '\n' {
						idx = i
						break
					}
				}
				if idx == -1 {
					lineBuf = append(lineBuf, chunk[start:]...)
					break
				}
				lineBuf = append(lineBuf, chunk[start:idx]...)
				h, decided, skip := smParseHeaderCandidate(string(lineBuf))
				lineBuf = lineBuf[:0]
				if decided && !skip {
					return h, nil
				}
				start = idx + 1
			}
		}
		if readErr == io.EOF {
			h, decided, skip := smParseHeaderCandidate(string(lineBuf))
			if decided && !skip {
				return h, nil
			}
			return nil, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	var probe [1]byte
	n, probeErr := f.Read(probe[:])
	if n == 0 && (probeErr == io.EOF || probeErr == nil) {
		h, decided, skip := smParseHeaderCandidate(string(lineBuf))
		if decided && !skip {
			return h, nil
		}
		return nil, nil
	}
	return nil, &smHeaderScanLimitError{filePath: filePath}
}

func smReadSessionHeaderForDiscovery(filePath string) *SessionHeader {
	h, err := smReadSessionHeader(filePath)
	if err != nil {
		return nil
	}
	return h
}

func smGetSessionHeaderCwd(header *SessionHeader) (string, bool) {
	if header == nil {
		return "", false
	}
	if header.Raw != nil {
		s, ok := header.Raw.GetString("cwd")
		return s, ok
	}
	return header.Cwd, true
}

func smSessionCwdMatches(cwd string, hasCwd bool, resolvedCwd string) bool {
	if !hasCwd || cwd == "" {
		return false
	}
	return utils.PathsEqual(smResolvePath(cwd), resolvedCwd)
}

// FindMostRecentSession returns the newest `.jsonl` session file in sessionDir (optionally matching cwd).
func FindMostRecentSession(sessionDir string, cwd ...string) *string {
	resolvedDir := smNormalizePath(sessionDir)
	var resolvedCwd string
	hasCwdFilter := len(cwd) > 0 && cwd[0] != ""
	if hasCwdFilter {
		resolvedCwd = smResolvePath(cwd[0])
	}

	dirEntries, err := os.ReadDir(resolvedDir)
	if err != nil {
		return nil
	}
	type candidate struct {
		path  string
		mtime int64
	}
	var candidates []candidate
	for _, de := range dirEntries {
		if !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(resolvedDir, de.Name())
		st, err := os.Stat(p)
		if err != nil {
			return nil
		}
		candidates = append(candidates, candidate{path: p, mtime: st.ModTime().UnixNano()})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		if a.mtime > b.mtime {
			return -1
		}
		if a.mtime < b.mtime {
			return 1
		}
		return 0
	})
	for _, c := range candidates {
		header := smReadSessionHeaderForDiscovery(c.path)
		if header == nil {
			continue
		}
		if hasCwdFilter {
			hCwd, ok := smGetSessionHeaderCwd(header)
			if !smSessionCwdMatches(hCwd, ok, resolvedCwd) {
				continue
			}
		}
		found := c.path
		return &found
	}
	return nil
}

func smExtractMessageText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return *m.Content.Text
		}
		return smJoinTextBlocks(m.Content.Blocks)
	case *ai.AssistantMessage:
		return smJoinTextBlocks(m.Content)
	}
	return ""
}

func smJoinTextBlocks(blocks ai.ContentList) string {
	var parts []string
	for _, b := range blocks {
		if tc, ok := b.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, " ")
}

func smMessageActivityTime(entry *SessionMessageEntry) (int64, bool) {
	if entry == nil || entry.Message == nil {
		return 0, false
	}
	role := entry.Message.GetRole()
	if role != "user" && role != "assistant" {
		return 0, false
	}
	// Distinguish explicit timestamp in raw JSON (including 0) vs omitted timestamp.
	if entry.Raw != nil {
		if msgVal, ok := entry.Raw.Get("message"); ok {
			if msgObj, isObj := msgVal.(*jsonx.Object); isObj && msgObj != nil {
				if tsVal, exists := msgObj.Get("timestamp"); exists {
					if f, isNum := tsVal.(float64); isNum {
						return int64(f), true
					}
				}
			}
		}
	}
	switch m := entry.Message.(type) {
	case *ai.UserMessage:
		if m.Timestamp != 0 {
			return m.Timestamp, true
		}
	case *ai.AssistantMessage:
		if m.Timestamp != 0 {
			return m.Timestamp, true
		}
	}
	return smParseTimeMs(entry.Timestamp)
}

func smBuildSessionInfo(ctx context.Context, filePath string, preStat os.FileInfo) (*SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := preStat
	if st == nil {
		var err error
		st, err = os.Stat(filePath)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, nil
		}
	}
	f, err := os.Open(filePath)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	defer func() { _ = f.Close() }()

	var header *SessionHeader
	messageCount := 0
	firstMessage := ""
	var allMessages []string
	var name *string
	var lastActivityTime int64
	hasLastActivity := false

	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			entry := smParseEntryLine(strings.TrimSuffix(line, "\n"))
			if entry != nil {
				if header == nil {
					h, ok := entry.(*SessionHeader)
					if !ok || h.Type != "session" {
						return nil, nil
					}
					header = h
				} else {
					switch e := entry.(type) {
					case *SessionInfoEntry:
						if e.Name != nil {
							trimmed := strings.TrimSpace(*e.Name)
							if trimmed == "" {
								name = nil
							} else {
								name = &trimmed
							}
						} else {
							name = nil
						}
					case *SessionMessageEntry:
						messageCount++
						if actMs, ok := smMessageActivityTime(e); ok {
							if !hasLastActivity || actMs > lastActivityTime {
								lastActivityTime = actMs
								hasLastActivity = true
							}
						}
						role := ""
						if e.Message != nil {
							role = e.Message.GetRole()
						}
						if role == "user" || role == "assistant" {
							text := smExtractMessageText(e.Message)
							if text != "" {
								allMessages = append(allMessages, text)
								if firstMessage == "" && role == "user" {
									firstMessage = text
								}
							}
						}
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if header == nil {
		return nil, nil
	}

	headerCwd, hasHeaderCwd := smGetSessionHeaderCwd(header)
	cwd := ""
	if hasHeaderCwd {
		cwd = headerCwd
	}

	created, _ := smParseISOTime(header.Timestamp)
	modified := st.ModTime()
	if hasLastActivity && lastActivityTime > 0 {
		modified = time.UnixMilli(lastActivityTime).UTC()
	} else if !created.IsZero() {
		modified = created
	}

	if firstMessage == "" {
		firstMessage = "(no messages)"
	}

	return &SessionInfo{
		Path:              filePath,
		Id:                header.Id,
		Cwd:               cwd,
		Name:              name,
		ParentSessionPath: smCopyStrPtr(header.ParentSession),
		Created:           created,
		Modified:          modified,
		MessageCount:      messageCount,
		FirstMessage:      firstMessage,
		AllMessagesText:   strings.Join(allMessages, " "),
	}, nil
}

type smFileCandidate struct {
	path  string
	stats os.FileInfo
}

func smSortSessionInfos(sessions []SessionInfo) []SessionInfo {
	slices.SortStableFunc(sessions, func(a, b SessionInfo) int {
		am, bm := a.Modified.UnixNano(), b.Modified.UnixNano()
		if bm > am {
			return 1
		}
		if bm < am {
			return -1
		}
		return 0
	})
	return sessions
}

func smMapWithConcurrency[T any, R any](
	ctx context.Context,
	items []T,
	limit int,
	fn func(ctx context.Context, item T, index int) (R, error),
) ([]R, error) {
	results := make([]R, len(items))
	if len(items) == 0 {
		return results, nil
	}
	workers := limit
	if workers > len(items) {
		workers = len(items)
	}
	var mu sync.Mutex
	nextIdx := 0
	var firstErr error
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				mu.Lock()
				if firstErr != nil || nextIdx >= len(items) {
					mu.Unlock()
					return
				}
				idx := nextIdx
				nextIdx++
				mu.Unlock()

				res, err := fn(ctx, items[idx], idx)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				results[idx] = res
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func smListSessionsFromDir(
	ctx context.Context,
	dir string,
	onProgress SessionListProgress,
) ([]SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return []SessionInfo{}, nil
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return []SessionInfo{}, nil
	}
	var names []string
	for _, de := range dirEntries {
		if strings.HasSuffix(de.Name(), ".jsonl") {
			names = append(names, de.Name())
		}
	}
	slices.SortStableFunc(names, func(a, b string) int {
		return strings.Compare(b, a)
	})
	candidates := make([]smFileCandidate, len(names))
	for i, n := range names {
		candidates[i] = smFileCandidate{path: filepath.Join(dir, n)}
	}

	total := len(candidates)
	var progMu sync.Mutex
	var partial []SessionInfo
	loaded := 0

	infos, err := smMapWithConcurrency(ctx, candidates, smMaxConcurrentSessionInfoLoads, func(ctx context.Context, c smFileCandidate, _ int) (*SessionInfo, error) {
		info, err := smBuildSessionInfo(ctx, c.path, c.stats)
		if err != nil {
			return nil, err
		}
		if onProgress != nil {
			progMu.Lock()
			loaded++
			curLoaded := loaded
			if info != nil {
				partial = append(partial, *info)
			}
			var snapshot []SessionInfo
			if curLoaded == 1 || curLoaded%smCurrentListPublishInterval == 0 || curLoaded == total {
				snapshot = make([]SessionInfo, len(partial))
				copy(snapshot, partial)
				smSortSessionInfos(snapshot)
			}
			// Serialize progress callbacks with progMu so callers observe monotonic loaded counts cleanly.
			onProgress(curLoaded, total, snapshot)
			progMu.Unlock()
		}
		return info, nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]SessionInfo, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			out = append(out, *info)
		}
	}
	return out, nil
}

// SessionManager manages append-only conversation session trees stored in JSONL files.
type SessionManager struct {
	sessionId           string
	sessionFile         *string
	sessionDir          string
	cwd                 string
	persist             bool
	flushed             bool
	fileEntries         []FileEntry
	byId                *omap.Map[string, SessionEntry]
	labelsById          *omap.Map[string, string]
	labelTimestampsById *omap.Map[string, string]
	leafId              *string
}

func smNewSessionManager(
	cwd string,
	sessionDir string,
	sessionFile *string,
	persist bool,
	newSessionOptions *NewSessionOptions,
	preloadedFileEntries []FileEntry,
) (*SessionManager, error) {
	m := &SessionManager{
		cwd:                 smResolvePath(cwd),
		sessionDir:          smNormalizePath(sessionDir),
		persist:             persist,
		byId:                omap.NewMap[string, SessionEntry](),
		labelsById:          omap.NewMap[string, string](),
		labelTimestampsById: omap.NewMap[string, string](),
	}
	if persist && m.sessionDir != "" {
		if _, err := os.Stat(m.sessionDir); os.IsNotExist(err) {
			if err := os.MkdirAll(m.sessionDir, 0o755); err != nil {
				return nil, err
			}
		}
	}

	if sessionFile != nil && *sessionFile != "" {
		if err := m.setSessionFile(*sessionFile, preloadedFileEntries); err != nil {
			return nil, err
		}
	} else if len(preloadedFileEntries) > 0 {
		if err := m.loadEntries(preloadedFileEntries, newSessionOptions); err != nil {
			return nil, err
		}
	} else {
		if _, err := m.NewSession(newSessionOptions); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// SessionManagerCreate opens a new persisted session in sessionDir (or the default cwd session dir).
func SessionManagerCreate(cwd string, sessionDir string, options ...*NewSessionOptions) (*SessionManager, error) {
	dir := GetDefaultSessionDir(cwd)
	if sessionDir != "" {
		dir = smNormalizePath(sessionDir)
	}
	var opts *NewSessionOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return smNewSessionManager(cwd, dir, nil, true, opts, nil)
}

// SessionManagerOpen loads an existing session file (or prepares a new file at path).
func SessionManagerOpen(path string, options ...SessionOpenOptions) (*SessionManager, error) {
	resolvedPath := smResolvePath(path)
	var opt SessionOpenOptions
	if len(options) > 0 {
		opt = options[0]
	}

	var header *SessionHeader
	var preloaded []FileEntry
	if opt.CwdOverride == nil {
		if _, err := os.Stat(resolvedPath); err == nil {
			h, readErr := smReadSessionHeader(resolvedPath)
			if readErr != nil {
				var scanErr *smHeaderScanLimitError
				if !errors.As(readErr, &scanErr) {
					return nil, readErr
				}
				preloaded = LoadEntriesFromFile(resolvedPath)
				if len(preloaded) > 0 {
					if first, ok := preloaded[0].(*SessionHeader); ok {
						header = first
					}
				}
			} else {
				header = h
			}
		}
	}

	cwd := ""
	if opt.CwdOverride != nil {
		cwd = *opt.CwdOverride
	} else if hCwd, ok := smGetSessionHeaderCwd(header); ok {
		cwd = hCwd
	} else {
		wd, err := os.Getwd()
		if err != nil {
			wd = "."
		}
		cwd = wd
	}

	dir := filepath.Dir(resolvedPath)
	if opt.SessionDir != "" {
		dir = smNormalizePath(opt.SessionDir)
	}
	return smNewSessionManager(cwd, dir, &resolvedPath, true, nil, preloaded)
}

// SessionManagerContinueRecent resumes the most recent matching session or starts a new one.
func SessionManagerContinueRecent(cwd string, sessionDir ...string) (*SessionManager, error) {
	dir := GetDefaultSessionDir(cwd)
	explicitDir := len(sessionDir) > 0 && sessionDir[0] != ""
	if explicitDir {
		dir = smNormalizePath(sessionDir[0])
	}
	filterCwd := explicitDir && !utils.PathsEqual(dir, smGetDefaultSessionDirPath(cwd))
	var recent *string
	if filterCwd {
		recent = FindMostRecentSession(dir, cwd)
	} else {
		recent = FindMostRecentSession(dir)
	}
	if recent != nil {
		return smNewSessionManager(cwd, dir, recent, true, nil, nil)
	}
	return smNewSessionManager(cwd, dir, nil, true, nil, nil)
}

// SessionManagerInMemory creates an in-memory session with no disk persistence.
func SessionManagerInMemory(cwd ...string) *SessionManager {
	dir := ""
	if len(cwd) > 0 && cwd[0] != "" {
		dir = cwd[0]
	} else if wd, err := os.Getwd(); err == nil {
		dir = wd
	} else {
		dir = "."
	}
	sm, _ := smNewSessionManager(dir, "", nil, false, nil, nil)
	return sm
}

// SessionManagerInMemoryWith creates an in-memory session initialized with explicit options or entries.
func SessionManagerInMemoryWith(cwd string, options *NewSessionOptions, entries []FileEntry) (*SessionManager, error) {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		} else {
			cwd = "."
		}
	}
	return smNewSessionManager(cwd, "", nil, false, options, entries)
}

// SessionManagerForkFrom copies sourcePath into a new session file rooted in targetCwd.
func SessionManagerForkFrom(sourcePath string, targetCwd string, sessionDir string, options ...*NewSessionOptions) (*SessionManager, error) {
	resolvedSource := smResolvePath(sourcePath)
	resolvedTargetCwd := smResolvePath(targetCwd)
	sourceEntries := LoadEntriesFromFile(resolvedSource)
	if len(sourceEntries) == 0 {
		return nil, fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", resolvedSource)
	}
	var sourceHeader *SessionHeader
	for _, e := range sourceEntries {
		if h, ok := e.(*SessionHeader); ok {
			sourceHeader = h
			break
		}
	}
	if sourceHeader == nil {
		return nil, fmt.Errorf("Cannot fork: source session has no header: %s", resolvedSource)
	}

	dir := GetDefaultSessionDir(resolvedTargetCwd)
	if sessionDir != "" {
		dir = smNormalizePath(sessionDir)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	var opts *NewSessionOptions
	if len(options) > 0 {
		opts = options[0]
	}
	var newSessionId string
	if opts != nil && opts.Id != nil {
		if err := AssertValidSessionId(*opts.Id); err != nil {
			return nil, err
		}
		newSessionId = *opts.Id
	} else {
		newSessionId = smCreateSessionId()
	}

	timestamp := smNowISO()
	fileTimestamp := smFilenameSafeTimeRe.ReplaceAllString(timestamp, "-")
	newSessionFile := filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newSessionId))

	ver := CurrentSessionVersion
	newHeader := &SessionHeader{
		Type:          "session",
		Version:       &ver,
		Id:            newSessionId,
		Timestamp:     timestamp,
		Cwd:           resolvedTargetCwd,
		ParentSession: &resolvedSource,
	}

	f, err := os.OpenFile(newSessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}
	writeErr := func() error {
		headerLine, err := newHeader.marshalJsonLine()
		if err != nil {
			return err
		}
		if _, err := io.WriteString(f, headerLine+"\n"); err != nil {
			return err
		}
		for _, entry := range sourceEntries {
			if entry.GetType() == "session" {
				continue
			}
			line, err := entry.marshalJsonLine()
			if err != nil {
				return err
			}
			if _, err := io.WriteString(f, line+"\n"); err != nil {
				return err
			}
		}
		return nil
	}()
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	return smNewSessionManager(resolvedTargetCwd, dir, &newSessionFile, true, nil, nil)
}

// SessionManagerFindById scans sessionDir (or default cwd sessionDir) for an exact header ID match.
func SessionManagerFindById(cwd string, id string, sessionDir ...string) *string {
	dir := GetDefaultSessionDir(cwd)
	explicitDir := len(sessionDir) > 0 && sessionDir[0] != ""
	if explicitDir {
		dir = smNormalizePath(sessionDir[0])
	}
	filterCwd := explicitDir && !utils.PathsEqual(dir, smGetDefaultSessionDirPath(cwd))
	resolvedCwd := smResolvePath(cwd)

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, de := range dirEntries {
		if !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		p := filepath.Join(dir, de.Name())
		header := smReadSessionHeaderForDiscovery(p)
		if header == nil || header.Id != id {
			continue
		}
		if filterCwd {
			hCwd, ok := smGetSessionHeaderCwd(header)
			if !smSessionCwdMatches(hCwd, ok, resolvedCwd) {
				continue
			}
		}
		found := p
		return &found
	}
	return nil
}

// SessionManagerList lists sessions for cwd in sessionDir (or the default cwd session directory).
func SessionManagerList(ctx context.Context, cwd string, sessionDir string, onProgress ...SessionListProgress) ([]SessionInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dir := GetDefaultSessionDir(cwd)
	explicitDir := sessionDir != ""
	if explicitDir {
		dir = smNormalizePath(sessionDir)
	}
	filterCwd := explicitDir && !utils.PathsEqual(dir, smGetDefaultSessionDirPath(cwd))
	resolvedCwd := smResolvePath(cwd)
	include := func(s SessionInfo) bool {
		return !filterCwd || smSessionCwdMatches(s.Cwd, true, resolvedCwd)
	}

	var prog SessionListProgress
	if len(onProgress) > 0 && onProgress[0] != nil {
		cb := onProgress[0]
		prog = func(loaded, total int, partial []SessionInfo) {
			if partial == nil {
				cb(loaded, total, nil)
				return
			}
			filtered := make([]SessionInfo, 0, len(partial))
			for _, s := range partial {
				if include(s) {
					filtered = append(filtered, s)
				}
			}
			cb(loaded, total, filtered)
		}
	}

	sessions, err := smListSessionsFromDir(ctx, dir, prog)
	if err != nil {
		return nil, err
	}
	filtered := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if include(s) {
			filtered = append(filtered, s)
		}
	}
	return smSortSessionInfos(filtered), nil
}

// SessionManagerListAll lists sessions in sessionDir if specified, or across all directories in utils.GetSessionsDir().
func SessionManagerListAll(ctx context.Context, sessionDir string, onProgress ...SessionListProgress) ([]SessionInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var prog SessionListProgress
	if len(onProgress) > 0 {
		prog = onProgress[0]
	}

	if sessionDir != "" {
		dir := smNormalizePath(sessionDir)
		sessions, err := smListSessionsFromDir(ctx, dir, prog)
		if err != nil {
			return nil, err
		}
		return smSortSessionInfos(sessions), nil
	}

	sessionsDir := utils.GetSessionsDir()
	if _, err := os.Stat(sessionsDir); err != nil {
		return []SessionInfo{}, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return []SessionInfo{}, nil
	}
	var dirs []string
	for _, de := range entries {
		if de.IsDir() || (de.Type()&os.ModeSymlink != 0) {
			dirs = append(dirs, filepath.Join(sessionsDir, de.Name()))
		}
	}

	dirFiles, err := smMapWithConcurrency(ctx, dirs, smMaxConcurrentDiscoveryLoads, func(ctx context.Context, dir string, _ int) ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil
		}
		var out []string
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".jsonl") {
				out = append(out, filepath.Join(dir, f.Name()))
			}
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}

	var allFiles []string
	for _, group := range dirFiles {
		allFiles = append(allFiles, group...)
	}

	candidates, err := smMapWithConcurrency(ctx, allFiles, smMaxConcurrentDiscoveryLoads, func(ctx context.Context, p string, _ int) (smFileCandidate, error) {
		if err := ctx.Err(); err != nil {
			return smFileCandidate{}, err
		}
		st, statErr := os.Stat(p)
		if statErr != nil {
			return smFileCandidate{path: p}, nil
		}
		return smFileCandidate{path: p, stats: st}, nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(candidates, func(a, b smFileCandidate) int {
		if a.stats != nil && b.stats != nil {
			am, bm := a.stats.ModTime().UnixNano(), b.stats.ModTime().UnixNano()
			if bm > am {
				return 1
			}
			if bm < am {
				return -1
			}
		} else if b.stats != nil {
			return 1
		} else if a.stats != nil {
			return -1
		}
		return strings.Compare(filepath.Base(b.path), filepath.Base(a.path))
	})

	totalFiles := len(candidates)
	var progMu sync.Mutex
	loaded := 0
	firstCandidateLoaded := false
	var partial []SessionInfo

	infos, err := smMapWithConcurrency(ctx, candidates, smMaxConcurrentSessionInfoLoads, func(ctx context.Context, c smFileCandidate, index int) (*SessionInfo, error) {
		info, err := smBuildSessionInfo(ctx, c.path, c.stats)
		if err != nil {
			return nil, err
		}
		if prog != nil {
			progMu.Lock()
			loaded++
			curLoaded := loaded
			if index == 0 {
				firstCandidateLoaded = true
			}
			if info != nil {
				partial = append(partial, *info)
			}
			var snapshot []SessionInfo
			if firstCandidateLoaded && (index == 0 || curLoaded%smAllListPublishInterval == 0 || curLoaded == totalFiles) {
				snapshot = make([]SessionInfo, len(partial))
				copy(snapshot, partial)
				smSortSessionInfos(snapshot)
			}
			// Serialize progress callbacks with progMu so callers observe monotonic loaded counts cleanly.
			prog(curLoaded, totalFiles, snapshot)
			progMu.Unlock()
		}
		return info, nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]SessionInfo, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			out = append(out, *info)
		}
	}
	return smSortSessionInfos(out), nil
}

// SetSessionFile switches this SessionManager to another session file path.
func (m *SessionManager) SetSessionFile(sessionFile string) error {
	return m.setSessionFile(sessionFile, nil)
}

func (m *SessionManager) setSessionFile(sessionFile string, preloaded []FileEntry) error {
	resolved := smResolvePath(sessionFile)
	m.sessionFile = &resolved

	st, statErr := os.Stat(resolved)
	if statErr == nil {
		entries := preloaded
		if entries == nil {
			entries = LoadEntriesFromFile(resolved)
		}
		if len(entries) == 0 {
			if st.Size() > 0 {
				return fmt.Errorf("Session file is not a valid %s session: %s", utils.AppName, resolved)
			}
			if _, err := m.NewSession(); err != nil {
				return err
			}
			m.sessionFile = &resolved
			if err := m.rewriteFile(); err != nil {
				return err
			}
			m.flushed = true
			return nil
		}
		if err := m.loadEntries(entries, nil); err != nil {
			return err
		}
		m.flushed = true
		return nil
	}

	if _, err := m.NewSession(); err != nil {
		return err
	}
	m.sessionFile = &resolved
	return nil
}

// NewSession resets state to a fresh v3 header and computes its new session filename when persisting.
func (m *SessionManager) NewSession(options ...*NewSessionOptions) (*string, error) {
	var opts *NewSessionOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts != nil && opts.Id != nil {
		if err := AssertValidSessionId(*opts.Id); err != nil {
			return nil, err
		}
		m.sessionId = *opts.Id
	} else {
		m.sessionId = smCreateSessionId()
	}

	timestamp := smNowISO()
	ver := CurrentSessionVersion
	var parentSession *string
	if opts != nil {
		parentSession = smCopyStrPtr(opts.ParentSession)
	}
	header := &SessionHeader{
		Type:          "session",
		Version:       &ver,
		Id:            m.sessionId,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: parentSession,
	}

	m.fileEntries = []FileEntry{header}
	m.byId = omap.NewMap[string, SessionEntry]()
	m.labelsById = omap.NewMap[string, string]()
	m.labelTimestampsById = omap.NewMap[string, string]()
	m.leafId = nil
	m.flushed = false

	if m.persist {
		fileTimestamp := smFilenameSafeTimeRe.ReplaceAllString(timestamp, "-")
		sf := filepath.Join(m.sessionDir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, m.sessionId))
		m.sessionFile = &sf
	}
	return smCopyStrPtr(m.sessionFile), nil
}

func (m *SessionManager) loadEntries(entries []FileEntry, options *NewSessionOptions) error {
	var header *SessionHeader
	for _, e := range entries {
		if h, ok := e.(*SessionHeader); ok {
			header = h
			break
		}
	}
	if header != nil {
		m.fileEntries = append([]FileEntry(nil), entries...)
		m.sessionId = header.Id
		if smMigrateToCurrentVersion(m.fileEntries) {
			if err := m.rewriteFile(); err != nil {
				return err
			}
		}
	} else {
		if _, err := m.NewSession(options); err != nil {
			return err
		}
		m.fileEntries = append(m.fileEntries, entries...)
	}
	m.buildIndex()
	return nil
}

func (m *SessionManager) buildIndex() {
	m.byId = omap.NewMap[string, SessionEntry]()
	m.labelsById = omap.NewMap[string, string]()
	m.labelTimestampsById = omap.NewMap[string, string]()
	m.leafId = nil

	for _, entry := range m.fileEntries {
		se, ok := entry.(SessionEntry)
		if !ok {
			continue
		}
		id := se.GetId()
		m.byId.Set(id, se)
		m.leafId = &id
		if le, ok := se.(*LabelEntry); ok {
			if le.Label != nil && *le.Label != "" {
				m.labelsById.Set(le.TargetId, *le.Label)
				m.labelTimestampsById.Set(le.TargetId, le.Timestamp)
			} else {
				m.labelsById.Delete(le.TargetId)
				m.labelTimestampsById.Delete(le.TargetId)
			}
		}
	}
}

func (m *SessionManager) rewriteFile() error {
	if !m.persist || m.sessionFile == nil {
		return nil
	}
	f, err := os.OpenFile(*m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	var writeErr error
	for _, entry := range m.fileEntries {
		line, err := entry.marshalJsonLine()
		if err != nil {
			writeErr = err
			break
		}
		if _, err := io.WriteString(f, line+"\n"); err != nil {
			writeErr = err
			break
		}
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (m *SessionManager) hasConversation() bool {
	for _, entry := range m.fileEntries {
		msgEntry, ok := entry.(*SessionMessageEntry)
		if !ok || msgEntry.Message == nil {
			continue
		}
		role := msgEntry.Message.GetRole()
		if role == "user" || role == "assistant" {
			return true
		}
	}
	return false
}

func (m *SessionManager) persistEntry(entry SessionEntry) error {
	if !m.persist || m.sessionFile == nil {
		return nil
	}
	if !m.flushed {
		if !m.hasConversation() {
			return nil
		}
		f, err := os.OpenFile(*m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return err
		}
		var writeErr error
		for _, e := range m.fileEntries {
			line, err := e.marshalJsonLine()
			if err != nil {
				writeErr = err
				break
			}
			if _, err := io.WriteString(f, line+"\n"); err != nil {
				writeErr = err
				break
			}
		}
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		m.flushed = true
		return nil
	}

	line, err := entry.marshalJsonLine()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(f, line+"\n")
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (m *SessionManager) usedIds() map[string]struct{} {
	used := make(map[string]struct{}, m.byId.Len())
	for _, k := range m.byId.Keys() {
		used[k] = struct{}{}
	}
	return used
}

func (m *SessionManager) nextEntryId() string {
	return smGenerateId(m.usedIds())
}

func (m *SessionManager) appendEntry(entry SessionEntry) error {
	prevLeaf := m.leafId
	id := entry.GetId()
	m.fileEntries = append(m.fileEntries, entry)
	m.byId.Set(id, entry)
	m.leafId = &id

	if err := m.persistEntry(entry); err != nil {
		m.fileEntries = m.fileEntries[:len(m.fileEntries)-1]
		m.byId.Delete(id)
		m.leafId = prevLeaf
		return err
	}
	return nil
}

func (m *SessionManager) indexMap() map[string]SessionEntry {
	idx := make(map[string]SessionEntry, m.byId.Len())
	for k, v := range m.byId.All() {
		idx[k] = v
	}
	return idx
}

func (m *SessionManager) IsPersisted() bool {
	return m.persist
}

func (m *SessionManager) GetCwd() string {
	return m.cwd
}

func (m *SessionManager) GetSessionDir() string {
	return m.sessionDir
}

// UsesDefaultSessionDir reports whether SessionDir matches the default encoded path for Cwd.
func (m *SessionManager) UsesDefaultSessionDir() bool {
	return utils.PathsEqual(m.sessionDir, smGetDefaultSessionDirPath(m.cwd))
}

func (m *SessionManager) GetSessionId() string {
	return m.sessionId
}

func (m *SessionManager) GetSessionFile() *string {
	return smCopyStrPtr(m.sessionFile)
}

// AppendMessage appends a message child of the current leaf and returns its entry ID.
func (m *SessionManager) AppendMessage(message agent.AgentMessage) (string, error) {
	entry := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "message",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		Message: message,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendThinkingLevelChange appends a thinking_level_change entry and returns its ID.
func (m *SessionManager) AppendThinkingLevelChange(thinkingLevel string) (string, error) {
	entry := &ThinkingLevelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "thinking_level_change",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		ThinkingLevel: thinkingLevel,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendModelChange appends a model_change entry and returns its ID.
func (m *SessionManager) AppendModelChange(provider, modelId string) (string, error) {
	entry := &ModelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "model_change",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		Provider: provider,
		ModelId:  modelId,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendUsage appends a non-context UsageEntry and returns the created entry.
func (m *SessionManager) AppendUsage(kind, provider, model string, usage ai.Usage, note ...string) (*UsageEntry, error) {
	var notePtr *string
	if len(note) > 0 && note[0] != "" {
		n := note[0]
		notePtr = &n
	}
	entry := &UsageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "usage",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		Kind:     kind,
		Provider: provider,
		Model:    model,
		Usage:    usage,
		Note:     notePtr,
	}
	if err := m.appendEntry(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// AppendCompaction appends a CompactionEntry capturing current system prompt state and returns its ID.
func (m *SessionManager) AppendCompaction(
	summary string,
	firstKeptEntryId *string,
	tokensBefore int,
	options ...*AppendCompactionOptions,
) (string, error) {
	timestamp := smNowISO()
	proj := m.BuildSessionProjection()
	var aiMsgs []ai.Message
	for _, msg := range proj.Messages {
		if am, ok := msg.(ai.Message); ok {
			aiMsgs = append(aiMsgs, am)
		}
	}
	sysMsg := ai.GetCurrentSystemMessage(aiMsgs)
	var stampedSysMsg *ai.SystemMessage
	if sysMsg != nil {
		cp := *sysMsg
		if ms, ok := smParseTimeMs(timestamp); ok {
			cp.Timestamp = ms
		}
		stampedSysMsg = &cp
	}

	id := m.nextEntryId()
	keptId := id
	if firstKeptEntryId != nil && *firstKeptEntryId != "" {
		keptId = *firstKeptEntryId
	}

	var opt *AppendCompactionOptions
	if len(options) > 0 {
		opt = options[0]
	}

	entry := &CompactionEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "compaction",
			Id:        id,
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: timestamp,
		},
		Summary:          summary,
		FirstKeptEntryId: keptId,
		TokensBefore:     tokensBefore,
		SystemMessage:    stampedSysMsg,
	}
	if opt != nil {
		entry.Details = opt.Details
		entry.Usage = opt.Usage
		entry.FromHook = smCopyBoolPtr(opt.FromHook)
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendCustomEntry appends an extension state entry (not included in LLM context) and returns its ID.
func (m *SessionManager) AppendCustomEntry(customType string, data ...any) (string, error) {
	var dataVal any
	if len(data) > 0 {
		dataVal = data[0]
	}
	entry := &CustomEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "custom",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		CustomType: customType,
		Data:       dataVal,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendSessionInfo records a sanitized display name for the session and returns its ID.
func (m *SessionManager) AppendSessionInfo(name string) (string, error) {
	sanitized := strings.TrimSpace(smLineBreakRe.ReplaceAllString(name, " "))
	entry := &SessionInfoEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "session_info",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		Name: &sanitized,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// GetSessionName returns the latest non-empty session name, or nil if cleared or unset.
func (m *SessionManager) GetSessionName() *string {
	for i := len(m.fileEntries) - 1; i >= 0; i-- {
		if info, ok := m.fileEntries[i].(*SessionInfoEntry); ok {
			if info.Name == nil {
				return nil
			}
			trimmed := strings.TrimSpace(*info.Name)
			if trimmed == "" {
				return nil
			}
			return &trimmed
		}
	}
	return nil
}

func smToUserMessageContent(v any) (ai.UserMessageContent, error) {
	switch c := v.(type) {
	case nil:
		return ai.UserMessageContent{}, nil
	case ai.UserMessageContent:
		return c, nil
	case *ai.UserMessageContent:
		if c == nil {
			return ai.UserMessageContent{}, nil
		}
		return *c, nil
	case string:
		s := c
		return ai.UserMessageContent{Text: &s}, nil
	case *string:
		if c == nil {
			return ai.UserMessageContent{}, nil
		}
		s := *c
		return ai.UserMessageContent{Text: &s}, nil
	case ai.ContentList:
		return ai.UserMessageContent{Blocks: c}, nil
	case []ai.Content:
		return ai.UserMessageContent{Blocks: ai.ContentList(c)}, nil
	default:
		var out ai.UserMessageContent
		b, err := jsonx.Marshal(v)
		if err != nil {
			return ai.UserMessageContent{}, err
		}
		if err := jsonx.Unmarshal(b, &out); err != nil {
			return ai.UserMessageContent{}, err
		}
		return out, nil
	}
}

// AppendCustomMessageEntry appends a custom_message entry participating in LLM context and returns its ID.
func (m *SessionManager) AppendCustomMessageEntry(customType string, content any, display bool, details ...any) (string, error) {
	umc, err := smToUserMessageContent(content)
	if err != nil {
		return "", err
	}
	var detailsVal any
	if len(details) > 0 {
		detailsVal = details[0]
	}
	entry := &CustomMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "custom_message",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		CustomType: customType,
		Content:    umc,
		Details:    detailsVal,
		Display:    display,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// AppendContextEdit appends a branch-local omission or content replacement for targetId.
func (m *SessionManager) AppendContextEdit(targetId string, replacement *ContextEditReplacement) (string, error) {
	if replacement != nil && replacement.Content.Text == nil && replacement.Content.Blocks == nil {
		return "", errors.New("Context edit replacement must be null or contain string/array content")
	}
	target, exists := m.byId.Get(targetId)
	if !exists || target == nil {
		return "", fmt.Errorf("Entry %s not found", targetId)
	}
	onBranch := false
	for _, entry := range m.GetBranch() {
		if entry.GetId() == targetId {
			onBranch = true
			break
		}
	}
	if !onBranch {
		return "", fmt.Errorf("Entry %s is not on the active branch", targetId)
	}

	targetRole := ""
	editable := false
	switch t := target.(type) {
	case *CustomMessageEntry:
		editable = true
		targetRole = "custom"
	case *SessionMessageEntry:
		if t.Message != nil {
			targetRole = t.Message.GetRole()
			editable = targetRole == "user" || targetRole == "assistant" || targetRole == "toolResult"
		}
	}
	if !editable {
		return "", fmt.Errorf("Entry %s does not contribute editable model content", targetId)
	}

	normReplacement := replacement
	if replacement != nil && (targetRole == "assistant" || targetRole == "toolResult") && replacement.Content.Text != nil {
		normReplacement = &ContextEditReplacement{
			Content: ai.UserMessageContent{
				Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: *replacement.Content.Text}},
			},
		}
	}

	entry := &ContextEditEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "context_edit",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		TargetId:    targetId,
		Replacement: normReplacement,
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

func (m *SessionManager) GetLeafId() *string {
	return smCopyStrPtr(m.leafId)
}

func (m *SessionManager) GetLeafEntry() SessionEntry {
	if m.leafId == nil {
		return nil
	}
	entry, _ := m.byId.Get(*m.leafId)
	return entry
}

func (m *SessionManager) GetEntry(id string) SessionEntry {
	entry, _ := m.byId.Get(id)
	return entry
}

func (m *SessionManager) GetChildren(parentId string) []SessionEntry {
	var children []SessionEntry
	for _, entry := range m.fileEntries {
		se, ok := entry.(SessionEntry)
		if !ok {
			continue
		}
		if pid := se.GetParentId(); pid != nil && *pid == parentId {
			children = append(children, se)
		}
	}
	return children
}

func (m *SessionManager) GetLabel(id string) *string {
	lbl, ok := m.labelsById.Get(id)
	if !ok {
		return nil
	}
	return &lbl
}

// AppendLabelChange sets or clears a bookmark label on targetId.
func (m *SessionManager) AppendLabelChange(targetId string, label *string) (string, error) {
	if _, exists := m.byId.Get(targetId); !exists {
		return "", fmt.Errorf("Entry %s not found", targetId)
	}
	entry := &LabelEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "label",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(m.leafId),
			Timestamp: smNowISO(),
		},
		TargetId: targetId,
		Label:    smCopyStrPtr(label),
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	if label != nil && *label != "" {
		m.labelsById.Set(targetId, *label)
		m.labelTimestampsById.Set(targetId, entry.Timestamp)
	} else {
		m.labelsById.Delete(targetId)
		m.labelTimestampsById.Delete(targetId)
	}
	return entry.Id, nil
}

// GetBranch returns entries from root to fromId (defaulting to current leafId).
func (m *SessionManager) GetBranch(fromId ...string) []SessionEntry {
	var startId *string
	if len(fromId) > 0 && fromId[0] != "" {
		s := fromId[0]
		startId = &s
	} else {
		startId = m.leafId
	}
	var path []SessionEntry
	var cur SessionEntry
	if startId != nil && *startId != "" {
		cur, _ = m.byId.Get(*startId)
	}
	for cur != nil {
		path = append(path, cur)
		pid := cur.GetParentId()
		if pid == nil || *pid == "" {
			break
		}
		cur, _ = m.byId.Get(*pid)
	}
	slices.Reverse(path)
	return path
}

func (m *SessionManager) BuildContextEntries() []SessionEntry {
	return smBuildSessionPathAndContextEntries(m.GetEntries(), m.leafId, m.indexMap())
}

func smBuildSessionPathAndContextEntries(entries []SessionEntry, leafId *string, idx map[string]SessionEntry) []SessionEntry {
	path := smBuildSessionPath(entries, leafId, leafId == nil, idx)
	return smBuildContextEntriesFromPath(path)
}

func (m *SessionManager) BuildSessionProjection() SessionProjection {
	return smBuildSessionProjection(m.GetEntries(), m.leafId, m.leafId == nil, m.indexMap())
}

func (m *SessionManager) BuildSessionContext() SessionContext {
	proj := m.BuildSessionProjection()
	return SessionContext{
		Messages:      proj.Messages,
		ThinkingLevel: proj.ThinkingLevel,
		Model:         proj.Model,
	}
}

func (m *SessionManager) GetHeader() *SessionHeader {
	for _, entry := range m.fileEntries {
		if h, ok := entry.(*SessionHeader); ok {
			return h
		}
	}
	return nil
}

func (m *SessionManager) GetEntryCount() int {
	return m.byId.Len()
}

func (m *SessionManager) GetEntries() []SessionEntry {
	out := make([]SessionEntry, 0, m.byId.Len())
	for _, entry := range m.fileEntries {
		if se, ok := entry.(SessionEntry); ok {
			out = append(out, se)
		}
	}
	return out
}

// GetTree returns the session entries organized as timestamp-sorted root trees.
func (m *SessionManager) GetTree() []SessionTreeNode {
	entries := m.GetEntries()
	nodes := make([]SessionTreeNode, len(entries))
	nodeById := make(map[string]*SessionTreeNode, len(entries))

	for i, entry := range entries {
		id := entry.GetId()
		var lbl *string
		if l, ok := m.labelsById.Get(id); ok {
			lbl = &l
		}
		var lblTs *string
		if lt, ok := m.labelTimestampsById.Get(id); ok {
			lblTs = &lt
		}
		nodes[i] = SessionTreeNode{
			Entry:          entry,
			Children:       []SessionTreeNode{},
			Label:          lbl,
			LabelTimestamp: lblTs,
		}
		nodeById[id] = &nodes[i]
	}

	var rootIds []string
	childIdsByParent := make(map[string][]string, len(entries))
	for _, entry := range entries {
		id := entry.GetId()
		pid := entry.GetParentId()
		if pid == nil || *pid == id {
			rootIds = append(rootIds, id)
		} else if _, hasParent := nodeById[*pid]; hasParent {
			childIdsByParent[*pid] = append(childIdsByParent[*pid], id)
		} else {
			rootIds = append(rootIds, id)
		}
	}

	var buildNode func(id string) SessionTreeNode
	buildNode = func(id string) SessionTreeNode {
		base := *nodeById[id]
		cids := childIdsByParent[id]
		children := make([]SessionTreeNode, len(cids))
		for i, cid := range cids {
			children[i] = buildNode(cid)
		}
		slices.SortStableFunc(children, func(a, b SessionTreeNode) int {
			at, aOk := smParseTimeMs(a.Entry.GetTimestamp())
			bt, bOk := smParseTimeMs(b.Entry.GetTimestamp())
			if !aOk || !bOk {
				return 0
			}
			if at < bt {
				return -1
			}
			if at > bt {
				return 1
			}
			return 0
		})
		base.Children = children
		return base
	}

	roots := make([]SessionTreeNode, len(rootIds))
	for i, rid := range rootIds {
		roots[i] = buildNode(rid)
	}
	return roots
}

// Branch moves the active leaf pointer to branchFromId without modifying history.
func (m *SessionManager) Branch(branchFromId string) error {
	if _, exists := m.byId.Get(branchFromId); !exists {
		return fmt.Errorf("Entry %s not found", branchFromId)
	}
	id := branchFromId
	m.leafId = &id
	return nil
}

// ResetLeaf clears the leaf pointer so the next appended entry becomes a new tree root.
func (m *SessionManager) ResetLeaf() {
	m.leafId = nil
}

// BranchWithSummary switches to branchFromId (nil for root) and appends a branch_summary entry.
func (m *SessionManager) BranchWithSummary(
	branchFromId *string,
	summary string,
	options ...*BranchWithSummaryOptions,
) (string, error) {
	if branchFromId != nil {
		if _, exists := m.byId.Get(*branchFromId); !exists {
			return "", fmt.Errorf("Entry %s not found", *branchFromId)
		}
	}
	fromId := "root"
	if m.leafId != nil {
		fromId = *m.leafId
	}
	m.leafId = smCopyStrPtr(branchFromId)

	var opt *BranchWithSummaryOptions
	if len(options) > 0 {
		opt = options[0]
	}

	entry := &BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "branch_summary",
			Id:        m.nextEntryId(),
			ParentId:  smCopyStrPtr(branchFromId),
			Timestamp: smNowISO(),
		},
		FromId:  fromId,
		Summary: summary,
	}
	if opt != nil {
		entry.Details = opt.Details
		entry.Usage = opt.Usage
		entry.FromHook = smCopyBoolPtr(opt.FromHook)
	}
	if err := m.appendEntry(entry); err != nil {
		return "", err
	}
	return entry.Id, nil
}

// CreateBranchedSession extracts the root-to-leafId path into a new session (and file when persisted).
func (m *SessionManager) CreateBranchedSession(leafId string) (*string, error) {
	previousSessionFile := smCopyStrPtr(m.sessionFile)
	path := m.GetBranch(leafId)
	if len(path) == 0 {
		return nil, fmt.Errorf("Entry %s not found", leafId)
	}

	var pathWithoutLabels []SessionEntry
	replacementByLabelId := make(map[string]string)
	var pendingLabelIds []string
	var pathParentId *string

	for _, entry := range path {
		if entry.GetType() == "label" {
			pendingLabelIds = append(pendingLabelIds, entry.GetId())
			continue
		}
		for _, lid := range pendingLabelIds {
			replacementByLabelId[lid] = entry.GetId()
		}
		pendingLabelIds = nil

		cloned := entry.cloneWithParentId(pathParentId)
		if comp, ok := cloned.(*CompactionEntry); ok {
			if comp.FirstKeptEntryId != comp.Id {
				if repl, exists := replacementByLabelId[comp.FirstKeptEntryId]; exists {
					comp.FirstKeptEntryId = repl
					if comp.Raw != nil {
						comp.Raw.Set("firstKeptEntryId", repl)
					}
				}
			}
		}
		pathWithoutLabels = append(pathWithoutLabels, cloned)
		eid := entry.GetId()
		pathParentId = &eid
	}

	newSessionId := smCreateSessionId()
	timestamp := smNowISO()
	fileTimestamp := smFilenameSafeTimeRe.ReplaceAllString(timestamp, "-")
	newSessionFile := filepath.Join(m.sessionDir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newSessionId))

	ver := CurrentSessionVersion
	var parentSession *string
	if m.persist {
		parentSession = previousSessionFile
	}
	header := &SessionHeader{
		Type:          "session",
		Version:       &ver,
		Id:            newSessionId,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: parentSession,
	}

	pathEntryIds := make(map[string]struct{}, len(pathWithoutLabels))
	for _, e := range pathWithoutLabels {
		pathEntryIds[e.GetId()] = struct{}{}
	}
	type labelToWrite struct {
		targetId  string
		label     string
		timestamp string
	}
	var labelsToWrite []labelToWrite
	for targetId, label := range m.labelsById.All() {
		if _, inPath := pathEntryIds[targetId]; inPath {
			ts, _ := m.labelTimestampsById.Get(targetId)
			labelsToWrite = append(labelsToWrite, labelToWrite{
				targetId:  targetId,
				label:     label,
				timestamp: ts,
			})
		}
	}

	var parentId *string
	if len(pathWithoutLabels) > 0 {
		lastId := pathWithoutLabels[len(pathWithoutLabels)-1].GetId()
		parentId = &lastId
	}

	labelEntries := make([]SessionEntry, 0, len(labelsToWrite))
	for _, lw := range labelsToWrite {
		lbl := lw.label
		le := &LabelEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      "label",
				Id:        smGenerateId(pathEntryIds),
				ParentId:  smCopyStrPtr(parentId),
				Timestamp: lw.timestamp,
			},
			TargetId: lw.targetId,
			Label:    &lbl,
		}
		pathEntryIds[le.Id] = struct{}{}
		labelEntries = append(labelEntries, le)
		nextParent := le.Id
		parentId = &nextParent
	}

	newFileEntries := make([]FileEntry, 0, 1+len(pathWithoutLabels)+len(labelEntries))
	newFileEntries = append(newFileEntries, header)
	for _, e := range pathWithoutLabels {
		newFileEntries = append(newFileEntries, e)
	}
	for _, le := range labelEntries {
		newFileEntries = append(newFileEntries, le)
	}

	if m.persist {
		m.fileEntries = newFileEntries
		m.sessionId = newSessionId
		m.sessionFile = &newSessionFile
		m.buildIndex()

		if m.hasConversation() {
			if err := m.rewriteFile(); err != nil {
				return nil, err
			}
			m.flushed = true
		} else {
			m.flushed = false
		}
		return &newSessionFile, nil
	}

	m.fileEntries = newFileEntries
	m.sessionId = newSessionId
	m.buildIndex()
	return nil, nil
}
