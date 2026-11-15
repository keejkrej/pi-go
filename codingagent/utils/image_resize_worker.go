// Ported from packages/coding-agent/src/utils/image-resize-worker.ts (pi v1.5.2).

package utils

// ImageResizeWorkerRequest mirrors the payload sent to the resize worker in TypeScript.
type ImageResizeWorkerRequest struct {
	ID       int
	Bytes    []byte
	MimeType string
	Options  *ImageResizeOptions
}

// ImageResizeWorkerResponse mirrors the result payload returned from the resize worker.
type ImageResizeWorkerResponse struct {
	ID     int
	Result *ResizedImage
	Error  error
}

// RunImageResizeWorker processes a resize request synchronously in Go using ResizeImageInProcess,
// cloning input bytes so callers retain an untouched input slice.
func RunImageResizeWorker(req ImageResizeWorkerRequest) ImageResizeWorkerResponse {
	cloned := make([]byte, len(req.Bytes))
	copy(cloned, req.Bytes)
	res, err := ResizeImageInProcess(cloned, req.MimeType, req.Options)
	return ImageResizeWorkerResponse{
		ID:     req.ID,
		Result: res,
		Error:  err,
	}
}
