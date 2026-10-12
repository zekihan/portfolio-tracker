package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func writeJSON(writer http.ResponseWriter, status int, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		slog.Error("JSON encoding failed", slog.Any("error", err))
		writer.WriteHeader(http.StatusInternalServerError)

		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)

	_, err = writer.Write(body)
	if err != nil {
		slog.Error("JSON response failed", slog.Any("error", err))
	}
}

func queryDefault(request *http.Request, key, fallback string) string {
	values, exists := request.URL.Query()[key]
	if !exists || len(values) == 0 {
		return fallback
	}

	return values[0]
}

func sendAttachment(
	writer http.ResponseWriter,
	request *http.Request,
	filename, downloadName string,
) {
	filenameEscaper := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	writer.Header().
		Set("Content-Disposition", `attachment; filename="`+filenameEscaper.Replace(downloadName)+`"`)
	http.ServeFile(writer, request, filename)
}

func notFound(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/plain")
	writer.WriteHeader(http.StatusNotFound)

	_, err := writer.Write([]byte("404 page not found"))
	if err != nil {
		slog.ErrorContext(request.Context(), "Not found response failed", slog.Any("error", err))
	}
}

// Preserve the existing method and trailing-slash behavior instead of ServeMux's
// implicit HEAD handlers and method-not-allowed responses.
func compatibleRoutes(router *http.ServeMux, static http.Handler) http.Handler {
	next := protectBrowserRequests()(router)
	missing := protectBrowserRequests()(http.HandlerFunc(notFound))
	serveStatic := protectBrowserRequests()(static)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCtx := request.Context()
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			if strings.HasPrefix(request.URL.Path, "/static/") {
				serveStatic.ServeHTTP(writer, request)

				return
			}

			if request.URL.Path == "/static" {
				target := "/static/"
				if request.URL.RawQuery != "" {
					target += "?" + request.URL.RawQuery
				}

				redirectLocal(writer, request, target)

				return
			}
		}

		if strings.Contains(request.URL.Path, "//") {
			missing.ServeHTTP(writer, request)

			return
		}

		request = routingRequest(request)

		_, pattern := router.Handler(request)
		if !matchesMethod(pattern, request.Method) {
			missing.ServeHTTP(writer, request)

			return
		}

		if pattern == "/" && request.URL.Path != "/" {
			alternate := request.Clone(requestCtx)

			alternate.URL.Path = strings.TrimSuffix(request.URL.Path, "/")
			if alternate.URL.Path == request.URL.Path {
				alternate.URL.Path += "/"
			}

			alternate = routingRequest(alternate)

			_, alternatePattern := router.Handler(alternate)
			if alternatePattern != "/" && matchesMethod(alternatePattern, request.Method) {
				redirectLocal(writer, request, alternate.URL.RequestURI())

				return
			}
		}

		next.ServeHTTP(writer, request)
	})
}

// Escape dot segments for routing so ServeMux doesn't clean them into different
// endpoints. PathValue still returns the original decoded value to the handler.
func routingRequest(request *http.Request) *http.Request {
	escaped := (&url.URL{Path: request.URL.Path}).EscapedPath()
	segments := strings.Split(escaped, "/")
	changed := false

	for index, segment := range segments {
		switch segment {
		case ".":
			segments[index] = "%2e"
			changed = true
		case "..":
			segments[index] = "%2e%2e"
			changed = true
		default:
		}
	}

	if !changed && request.URL.RawPath == "" {
		return request
	}

	request = request.Clone(request.Context())

	request.URL.RawPath = ""
	if changed {
		request.URL.RawPath = strings.Join(segments, "/")
	}

	return request
}

//nolint:gosec // Local path validation rejects external redirect targets.
func redirectLocal(writer http.ResponseWriter, request *http.Request, target string) {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		notFound(writer, request)

		return
	}

	prefix := forwardedPrefix(request)
	if prefix != "" && prefix != "/" {
		parsed, err := url.ParseRequestURI(target)
		if err != nil {
			notFound(writer, request)

			return
		}

		request = request.Clone(request.Context())
		request.URL.Path = prefix + "/" + parsed.Path
		request.URL.RawPath = ""
		target = request.URL.String()
	}

	status := http.StatusTemporaryRedirect
	if request.Method == http.MethodGet {
		status = http.StatusMovedPermanently
	}
	// The target is constrained to an absolute path on this server.
	http.Redirect(
		writer,
		request,
		target,
		status,
	)
}

func matchesMethod(pattern, method string) bool {
	if pattern == "/" {
		return true
	}

	return strings.HasPrefix(pattern, method+" ")
}

func forwardedPrefix(request *http.Request) string {
	prefix := path.Clean(request.Header.Get("X-Forwarded-Prefix"))
	if prefix == "." {
		return ""
	}

	prefix = strings.Map(func(character rune) rune {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '/' || character == '-' {
			return character
		}

		return -1
	}, prefix)
	for strings.Contains(prefix, "//") {
		prefix = strings.ReplaceAll(prefix, "//", "/")
	}

	return prefix
}
