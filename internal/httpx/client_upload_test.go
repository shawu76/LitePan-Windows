package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewUploadClientProtocolPolicy(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	base := server.Client()
	base.Transport.(*http.Transport).ForceAttemptHTTP2 = true

	for _, tc := range []struct {
		name      string
		http2     bool
		wantProto string
	}{
		{name: "default_http1", wantProto: "HTTP/1.1"},
		{name: "declared_http2", http2: true, wantProto: "HTTP/2.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewUploadClient(base, time.Second, tc.http2)
			defer client.CloseIdleConnections()
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.Proto != tc.wantProto {
				t.Fatalf("protocol = %s, want %s", resp.Proto, tc.wantProto)
			}
		})
	}
}
