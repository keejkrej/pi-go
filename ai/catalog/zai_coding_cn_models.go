// Ported from packages/ai/src/providers/zai-coding-cn.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var zccmChat, zccmImage, zccmClassifier = mgLoadProviderCatalog(ai.KnownProviderZaiCodingCn, "zai-coding-cn.json")

// ZaiCodingCnModels is the chat model catalog for zai-coding-cn.
var ZaiCodingCnModels ai.ChatModelCatalog = zccmChat

// ZaiCodingCnImageModels is the image model catalog for zai-coding-cn.
var ZaiCodingCnImageModels ai.ImageModelCatalog = zccmImage

// ZaiCodingCnClassifierModels is the classifier model catalog for zai-coding-cn.
var ZaiCodingCnClassifierModels ai.ClassifierModelCatalog = zccmClassifier
