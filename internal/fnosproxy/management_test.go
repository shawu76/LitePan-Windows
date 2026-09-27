package fnosproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"litepan/internal/settings"
)

func TestManagementLibrariesAndActions(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authx") == "" {
			t.Fatal("请求缺少 authx 签名")
		}
		mu.Lock()
		seen[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v/api/v2/user/loginByPassword":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"token": "test-token"}})
		case "/v/api/v1/mdb/list":
			if r.Header.Get("Authorization") != "test-token" {
				t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []map[string]any{{"guid": "lib-1", "name": "电影"}}})
		case "/v/api/v1/mdb/scan/lib-1", "/v/api/v1/mdb/refresh":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("操作请求体解析失败：%v", err)
			}
			key := "guid"
			if r.URL.Path == "/v/api/v1/mdb/refresh" {
				key = "mdb_guid"
			}
			if body[key] != "lib-1" {
				t.Fatalf("%s 请求体=%v", r.URL.Path, body)
			}
			if r.URL.Path == "/v/api/v1/mdb/refresh" && body["refresh_mode"] != float64(1) {
				t.Fatalf("刷新方式请求体=%v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	svc := testFnosProxyService(t, upstream.URL)
	if err := svc.settings.Update(t.Context(), map[string]string{
		settings.KeyFnosAdminUsername: "admin",
		settings.KeyFnosAdminPassword: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	libraries, err := svc.ListLibraries(t.Context())
	if err != nil || len(libraries) != 1 || libraries[0].ID != "lib-1" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	if err := svc.ScanLibrary(t.Context(), "lib-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshMetadata(t.Context(), "lib-1", 1); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"GET /v/api/v1/mdb/list", "POST /v/api/v1/mdb/scan/lib-1", "POST /v/api/v1/mdb/refresh"} {
		if seen[key] != 1 {
			t.Fatalf("%s calls=%d", key, seen[key])
		}
	}
}
