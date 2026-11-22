package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
)

type filesOnly struct {
	directory string
}

func (filesystem filesOnly) Open(name string) (http.File, error) {
	// URL paths use forward slashes. Reject OS-specific separators before
	// normalization so Windows cannot reinterpret an unchecked component.
	if strings.ContainsRune(name, '\\') {
		return nil, os.ErrNotExist
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+name), "/")
	for component := range strings.SplitSeq(cleaned, "/") {
		if strings.HasPrefix(component, ".") {
			return nil, os.ErrNotExist
		}
	}

	if cleaned == "" {
		cleaned = "."
	}

	file, err := os.OpenInRoot(filesystem.directory, cleaned)
	if err != nil {
		return nil, fmt.Errorf("open static file: %w", err)
	}

	return directoryHidden{File: file}, nil
}

type directoryHidden struct {
	http.File
}

func (directoryHidden) Readdir(_ int) ([]os.FileInfo, error) {
	return nil, nil
}

type staticResponse struct {
	http.ResponseWriter

	written bool
}

func (response *staticResponse) WriteHeader(status int) {
	if !response.written {
		response.written = true
		response.ResponseWriter.WriteHeader(status)
	}
}

func (response *staticResponse) Write(body []byte) (int, error) {
	if !response.written {
		response.WriteHeader(http.StatusNotFound)
	}

	written, err := response.ResponseWriter.Write(body)
	if err != nil {
		return written, fmt.Errorf("write static response: %w", err)
	}

	return written, nil
}

func staticFiles(root string) http.Handler {
	filesystem := filesOnly{directory: root}
	server := http.StripPrefix("/static", http.FileServer(filesystem))

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := strings.TrimPrefix(request.URL.Path, "/static")
		if path.Clean("/"+name) == "/offline.html" {
			// The service worker stores the offline document explicitly. A normal
			// navigation must check the server and its authentication proxy.
			writer.Header().Set("Cache-Control", "no-store")
		}

		file, err := filesystem.Open(name)
		if err != nil {
			writer.WriteHeader(http.StatusNotFound)

			return
		}

		closeErr := file.Close()
		if closeErr != nil {
			slog.ErrorContext(
				request.Context(),
				"Static file close failed",
				slog.Any("error", closeErr),
			)
		}

		response := &staticResponse{ResponseWriter: writer}
		server.ServeHTTP(response, request)
	})
}
