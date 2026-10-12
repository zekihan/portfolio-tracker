package db

import (
	"fmt"
	"net/url"
	"strings"
)

// sqlitePathURL encodes a native absolute filename without interpreting its query characters.
// Windows drive paths need a leading slash so the drive cannot become a URI authority.
func sqlitePathURL(path string, windows bool) *url.URL {
	if windows {
		path = strings.ReplaceAll(path, `\`, "/")
		if after, ok := strings.CutPrefix(path, "//?/UNC/"); ok {
			path = "//" + after
		} else {
			path = strings.TrimPrefix(path, "//?/")
		}

		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}

	return &url.URL{Scheme: "file", Path: path}
}

func windowsDrivePath(path string) bool {
	return len(path) >= 3 &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' &&
		(path[2] == '/' || path[2] == '\\')
}

// Explicit URI paths are already escaped; decode them exactly once before rebuilding.
func normalizeWindowsFileURL(uri *url.URL) error {
	if windowsDrivePath(uri.Opaque) {
		path, err := url.PathUnescape(uri.Opaque)
		if err != nil {
			return fmt.Errorf("decode Windows file URI: %w", err)
		}

		uri.Path = sqlitePathURL(path, true).Path
		uri.Opaque = ""
	} else if windowsDrivePath(strings.TrimPrefix(uri.Path, "/")) {
		uri.Path = sqlitePathURL(uri.Path, true).Path
		uri.RawPath = ""
	}

	return nil
}
