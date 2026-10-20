//go:build !windows

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func catalogCLI(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalog.py")
	script := "#!/usr/bin/env python3\nimport sys,json,time\n" + body
	if err := os.WriteFile(p, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestModelsHandshakePaginationAndAdvertisedEfforts(t *testing.T) {
	binary := catalogCLI(t, `req=json.loads(input())
assert req['method']=='initialize'
print(json.dumps({'id':req['id'],'result':{}}),flush=True)
assert json.loads(input())['method']=='initialized'
for page in range(2):
 r=json.loads(input())
 assert r['method']=='model/list'
 assert r['params']['includeHidden']==False
 if page: assert r['params']['cursor']=='next'
 model={'id':'picker-'+str(page),'model':'model-'+str(page),'displayName':'Model '+str(page),'hidden':False,'defaultReasoningEffort':'adaptive','supportedReasoningEfforts':[{'reasoningEffort':'adaptive','description':'Server-defined effort'},{'reasoningEffort':'none','description':'No reasoning'}],'isDefault':page==0}
 print(json.dumps({'method':'ignored/notification'}),flush=True)
 print(json.dumps({'id':r['id'],'result':{'data':[model],'nextCursor':'next' if page==0 else None}}),flush=True)
sys.stdin.read()
`)
	c := New(binary, "", time.Second)
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "model-0" || models[0].ReasoningEfforts[0].ID != "adaptive" || models[0].DefaultReasoning != "adaptive" || !models[0].IsDefault {
		t.Fatalf("bad catalog: %+v", models)
	}
}
func TestModelsTimeoutAndProtocolError(t *testing.T) {
	for _, body := range []string{"time.sleep(10)\n", "r=json.loads(input())\nprint(json.dumps({'id':r['id'],'error':{'code':-1,'message':'unsupported method'}}),flush=True)\nsys.stdin.read()\n"} {
		c := New(catalogCLI(t, body), "", time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		_, err := c.Models(ctx)
		cancel()
		if err == nil {
			t.Fatal("expected discovery error")
		}
	}
}

func TestModelsRejectMalformedCatalog(t *testing.T) {
	for _, result := range []string{
		`{"nextCursor":null}`,
		`{"data":[],"nextCursor":null}`,
		`{"data":[{"model":"x","displayName":"X","defaultReasoningEffort":"high","supportedReasoningEfforts":[]}],"nextCursor":null}`,
		`{"data":[{"model":"x","displayName":"X","supportedReasoningEfforts":[{"reasoningEffort":"high\nmodel=bad"}]}],"nextCursor":null}`,
	} {
		t.Run(result, func(t *testing.T) {
			body := `req=json.loads(input())
print(json.dumps({'id':req['id'],'result':{}}),flush=True)
json.loads(input())
r=json.loads(input())
print(json.dumps({'id':r['id'],'result':json.loads(` + strconv.Quote(result) + `)}),flush=True)
sys.stdin.read()
`
			_, err := New(catalogCLI(t, body), "", time.Second).Models(context.Background())
			if err == nil {
				t.Fatal("accepted malformed catalog")
			}
		})
	}
}
