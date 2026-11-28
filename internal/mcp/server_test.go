package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/client"
)

func serve(t *testing.T, handler http.HandlerFunc, lines ...string) []map[string]any {
	t.Helper()
	ts := httptest.NewServer(handler)
	defer ts.Close()
	s := New(&client.Client{BaseURL: ts.URL, Token: "t", Actor: "a", SessionID: "s", ClientHost: "h"}, "test")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		res = append(res, m)
	}
	return res
}

func TestHandshakeAndToolList(t *testing.T) {
	res := serve(t, func(http.ResponseWriter, *http.Request) { t.Error("no HTTP call expected") },
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
		`not json`,
	)
	if len(res) != 4 {
		t.Fatalf("a notification must get no reply; got %d replies: %v", len(res), res)
	}
	if res[0]["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Errorf("initialize: %v", res[0])
	}
	tools := res[1]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, x := range tools {
		names[x.(map[string]any)["name"].(string)] = true
	}
	for _, n := range []string{"agb_context", "agb_ready", "agb_edge_candidates", "agb_edge_decide", "agb_spec_put", "agb_record_add"} {
		if !names[n] {
			t.Errorf("tool %s missing", n)
		}
	}
	if res[2]["error"].(map[string]any)["code"] != float64(-32601) || res[3]["error"].(map[string]any)["code"] != float64(-32700) {
		t.Errorf("errors: %v %v", res[2], res[3])
	}
}

func call(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func TestToolsCallAPI(t *testing.T) {
	var seen []string
	var bodies []string
	h := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		bodies = append(bodies, string(b))
		if r.Header.Get("Authorization") != "Bearer t" || r.Header.Get("X-Actor") != "a" {
			t.Errorf("trace headers missing: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/reject") {
			w.WriteHeader(409)
			io.WriteString(w, `{"error":{"code":"conflict","message":"이미 결정한 간선입니다."}}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}
	res := serve(t, h,
		call(1, "agb_context", `{"query":"락 경합","project":"DEMO","max_results":2}`),
		call(2, "agb_ready", `{"project":"DEMO","limit":5}`),
		call(3, "agb_edge_decide", `{"id":9,"action":"confirm"}`),
		call(4, "agb_edge_decide", `{"id":9,"action":"reject"}`),
		call(5, "agb_spec_put", `{"project":"DEMO","slug":"api","content":"# x","wait":true}`),
		call(6, "agb_edge_decide", `{"id":9,"action":"drop"}`),
		call(7, "agb_context", `{}`),
	)
	want := []string{"POST /v1/context/query", "GET /v1/projects/DEMO/ready?limit=5", "POST /v1/edges/9/confirm", "POST /v1/edges/9/reject", "PUT /v1/projects/DEMO/specs/api?wait=true"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v", seen)
	}
	if !strings.Contains(bodies[0], `"query":"락 경합"`) || strings.Contains(bodies[0], "wait") {
		t.Errorf("context body = %s", bodies[0])
	}
	text := func(i int) (string, bool) {
		r := res[i]["result"].(map[string]any)
		e, _ := r["isError"].(bool)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), e
	}
	if s, e := text(0); e || s != `{"ok":true}` {
		t.Errorf("context result %q %v", s, e)
	}
	if s, e := text(3); !e || !strings.Contains(s, "409 conflict") {
		t.Errorf("api error must be a tool error: %q %v", s, e)
	}
	for _, i := range []int{5, 6} {
		if s, e := text(i); !e || !strings.Contains(s, "invalid argument") {
			t.Errorf("bad argument %d: %q %v", i, s, e)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("invalid arguments must not reach the API: %v", seen)
	}
}
