package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type filesOnly struct {
	directory http.Dir
}

func (filesystem filesOnly) Open(name string) (http.File, error) {
	file, err := filesystem.directory.Open(name)
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
	filesystem := filesOnly{directory: http.Dir(root)}
	server := http.StripPrefix("/static", http.FileServer(filesystem))

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		file, err := filesystem.Open(strings.TrimPrefix(request.URL.Path, "/static"))
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
