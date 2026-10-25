package main

import (
	"net"
	"testing"
)

func TestBackendListenerBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, host string
		loopback   bool
	}{
		{"default", "", true},
		{"IPv4 loopback", "127.0.0.1", true},
		{"IPv6 loopback", "::1", true},
		{"explicit IPv4 wildcard", "0.0.0.0", false},
		{"explicit IPv6 wildcard", "::", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			listener, err := listenBackend(t.Context(), test.host, "0")
			if err != nil {
				if test.host == "::1" || test.host == "::" {
					t.Skipf("IPv6 unavailable: %v", err)
				}

				t.Fatal(err)
			}
			defer listener.Close()

			address, ok := listener.Addr().(*net.TCPAddr)
			if !ok || address.IP.IsLoopback() != test.loopback {
				t.Fatalf("unexpected listener: %v", listener.Addr())
			}

			if !test.loopback && !address.IP.IsUnspecified() {
				t.Fatalf("wildcard not explicit: %v", address)
			}

			if test.host == "" && !address.IP.Equal(net.ParseIP(defaultBindAddress)) {
				t.Fatalf("default listener = %v", address)
			}
		})
	}
}

func TestBackendListenerRejectsInvalidHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"localhost", ":", "127.0.0.1:8080", "invalid"} {
		listener, err := listenBackend(t.Context(), host, "0")
		if err == nil {
			_ = listener.Close()

			t.Fatalf("invalid address accepted: %q", host)
		}
	}
}
