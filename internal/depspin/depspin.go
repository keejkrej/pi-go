//go:build depspin

// Package depspin pins every module listed in PORTING.md section 11 so that
// go mod tidy keeps them before any ported package imports them. It is never
// built: the depspin build tag is not set in normal builds. Remove an import
// here only after a real package imports a package from the same module.
package depspin

import (
	_ "github.com/alecthomas/chroma/v2"
	_ "github.com/alecthomas/chroma/v2/lexers"
	_ "github.com/alecthomas/chroma/v2/styles"
	_ "github.com/aws/aws-sdk-go-v2/aws"
	_ "github.com/aws/aws-sdk-go-v2/config"
	_ "github.com/aws/aws-sdk-go-v2/credentials"
	_ "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	_ "github.com/aws/smithy-go"
	_ "github.com/bmatcuk/doublestar/v4"
	_ "github.com/charmbracelet/x/vt"
	_ "github.com/coder/websocket"
	_ "github.com/disintegration/imaging"
	_ "github.com/dlclark/regexp2/v2"
	_ "github.com/evanw/esbuild/pkg/api"
	_ "github.com/fsnotify/fsnotify"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/google/uuid"
	_ "github.com/jezek/xgb"
	_ "github.com/jezek/xgb/xproto"
	_ "github.com/klauspost/compress/zstd"
	_ "github.com/rivo/uniseg"
	_ "github.com/tetratelabs/wazero"
	_ "github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	_ "go.yaml.in/yaml/v3"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "golang.org/x/net/http/httpproxy"
	_ "golang.org/x/oauth2"
	_ "golang.org/x/oauth2/google"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/sync/semaphore"
	_ "golang.org/x/sync/singleflight"
	_ "golang.org/x/term"
	_ "golang.org/x/text/cases"
	_ "golang.org/x/text/collate"
	_ "golang.org/x/text/language"
	_ "golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
)
