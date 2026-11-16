// Ported from packages/durable/src/session/forks.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
)

const sfScanPageSize = 256

// sfForkDocumentCopy is one definition-free document copy created with a forked conversation.
type sfForkDocumentCopy struct {
	Record DocumentCreate
	Source DocumentCopySource
}

// sfPrepareForkDocumentCopies selects every persisted conversation document copied by one fork.
func sfPrepareForkDocumentCopies(
	ctx chord.Context,
	storage Storage,
	parentConversationId ConversationId,
	at EntryId,
	childConversationId ConversationId,
) ([]sfForkDocumentCopy, error) {
	storedEntry, err := storage.EntryInConversation(ctx, parentConversationId, at)
	if err != nil {
		return nil, err
	}
	if storedEntry == nil {
		return nil, fmt.Errorf("Entry %d is not visible from conversation %d", at, parentConversationId)
	}

	var copies []sfForkDocumentCopy
	copiedAddresses := make(map[string]struct{})
	if err := sfCollectCopies(
		ctx,
		storage,
		DocumentScope{Kind: DocumentScopeConversation, ConversationId: storedEntry.Entry.ConversationId},
		DocumentPointSeq(storedEntry.CommitSeq),
		DocumentForkAsOf,
		childConversationId,
		&copies,
		copiedAddresses,
	); err != nil {
		return nil, err
	}
	if err := sfCollectCopies(
		ctx,
		storage,
		DocumentScope{Kind: DocumentScopeConversation, ConversationId: parentConversationId},
		DocumentPointCurrent(),
		DocumentForkCurrent,
		childConversationId,
		&copies,
		copiedAddresses,
	); err != nil {
		return nil, err
	}
	return copies, nil
}

func sfCollectCopies(
	ctx chord.Context,
	storage Storage,
	scope DocumentScope,
	at DocumentPoint,
	policy DocumentFork,
	childConversationId ConversationId,
	copies *[]sfForkDocumentCopy,
	copiedAddresses map[string]struct{},
) error {
	var cursor Cursor
	for {
		page, err := storage.ScanDocuments(ctx, DocumentQuery{Scope: scope, At: at}, sfScanPageSize, cursor)
		if err != nil {
			return err
		}
		for _, source := range page.Items {
			if source.Scope.Kind != DocumentScopeConversation || source.Fork != policy {
				continue
			}
			rawId, err := storage.MintId()
			if err != nil {
				return err
			}
			id := IdFromNumber[DocumentId](rawId)
			rec := DocumentCreate{
				Id:      id,
				Kind:    source.Kind,
				Key:     docuCopyStringPtr(source.Key),
				Scope:   DocumentScope{Kind: DocumentScopeConversation, ConversationId: childConversationId},
				History: source.History,
				Fork:    source.Fork,
			}
			copyAddress := AddressId(DocumentAddress{Kind: rec.Kind, Scope: rec.Scope, Key: rec.Key})
			if _, exists := copiedAddresses[copyAddress]; exists {
				member := rec.Kind
				if rec.Key != nil {
					member = fmt.Sprintf("%s/%s", rec.Kind, *rec.Key)
				}
				return fmt.Errorf("Fork selects multiple source documents for %s", member)
			}
			copiedAddresses[copyAddress] = struct{}{}
			*copies = append(*copies, sfForkDocumentCopy{
				Record: rec,
				Source: DocumentCopySource{Id: source.Id, At: at},
			})
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	return nil
}
