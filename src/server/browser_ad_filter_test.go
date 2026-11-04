package main

import (
	"testing"

	"github.com/dop251/goja"
)

func playerFilterVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	_, err := vm.RunString(`const window=globalThis; const location={hostname:'www.youtube.com',origin:'https://www.youtube.com',href:'https://www.youtube.com/watch?v=test'}; class URL {constructor(raw){const s=String(raw); this.origin=s.startsWith('https://other.example')?'https://other.example':location.origin; this.pathname=s.replace(/^https:\/\/[^/]+/,'').split('?')[0];}}`)
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func runPlayerFilter(t *testing.T, vm *goja.Runtime, script string) goja.Value {
	t.Helper()
	value, err := vm.RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPlayerDataFilterPreservesContentAndErrors(t *testing.T) {
	vm := playerFilterVM(t)
	runPlayerFilter(t, vm, browserAdFilterScript)
	runPlayerFilter(t, vm, `window.ytInitialPlayerResponse={adPlacements:[1],playerAds:[2],adSlots:[3],videoDetails:{videoId:'target'},streamingData:{serverAbrStreamingUrl:'media'},playabilityStatus:{status:'OK'}};`)
	got := runPlayerFilter(t, vm, `JSON.stringify(ytInitialPlayerResponse)`)
	if got.String() != `{"videoDetails":{"videoId":"target"},"streamingData":{"serverAbrStreamingUrl":"media"},"playabilityStatus":{"status":"OK"}}` {
		t.Fatalf("initial player data: %s", got)
	}
	got = runPlayerFilter(t, vm, `JSON.stringify(JSON.parse('{"playerResponse":{"adSlots":[1],"videoDetails":{"videoId":"target"},"playabilityStatus":{"status":"LOGIN_REQUIRED"}},"content":{"adSlots":["keep"]}}'))`)
	if got.String() != `{"playerResponse":{"videoDetails":{"videoId":"target"},"playabilityStatus":{"status":"LOGIN_REQUIRED"}},"content":{"adSlots":["keep"]}}` {
		t.Fatalf("nested player response: %s", got)
	}
	got = runPlayerFilter(t, vm, `JSON.stringify(JSON.parse('{"adSlots":["unrelated"]}'))`)
	if got.String() != `{"adSlots":["unrelated"]}` {
		t.Fatal("unrelated JSON was modified")
	}
	got = runPlayerFilter(t, vm, `let invalid=false; try { JSON.parse('not json'); } catch (e) {invalid=e instanceof SyntaxError;} invalid`)
	if !got.ToBoolean() {
		t.Fatal("JSON parse error was swallowed")
	}
}

func TestPlayerDataFilterScopesXHRAndPreservesFailureResponses(t *testing.T) {
	vm := playerFilterVM(t)
	runPlayerFilter(t, vm, `window.XMLHttpRequest=class {constructor(){this.readyState=4;this.status=200;this.responseType='';this.body='{"videoDetails":{"videoId":"target"},"adSlots":[1]}';} open(method,url){this.opened=url;} get response(){return this.responseType==='json'?{videoDetails:{videoId:'target'},adSlots:[1]}:this.body;} get responseText(){if(this.responseType==='json')throw new Error('InvalidStateError');return this.body;}};`)
	runPlayerFilter(t, vm, browserAdFilterScript)
	got := runPlayerFilter(t, vm, `const xhr=new XMLHttpRequest(); xhr.open('POST','/youtubei/v1/player?prettyPrint=false'); xhr.responseText`)
	if got.String() != `{"videoDetails":{"videoId":"target"}}` {
		t.Fatalf("player XHR: %s", got)
	}
	got = runPlayerFilter(t, vm, `xhr.responseType='json'; JSON.stringify(xhr.response)`)
	if got.String() != `{"videoDetails":{"videoId":"target"}}` {
		t.Fatal("JSON XHR was not filtered")
	}
	got = runPlayerFilter(t, vm, `xhr.responseType=''; xhr.status=403; xhr.responseText===xhr.body`)
	if !got.ToBoolean() {
		t.Fatal("HTTP failure payload changed")
	}
	got = runPlayerFilter(t, vm, `xhr.status=200; xhr.open('GET','https://other.example/youtubei/v1/player'); xhr.response===xhr.body`)
	if !got.ToBoolean() {
		t.Fatal("unrelated origin was filtered")
	}
	got = runPlayerFilter(t, vm, `xhr.open('GET','/api/player'); xhr.response===xhr.body`)
	if !got.ToBoolean() {
		t.Fatal("unrelated API was filtered")
	}
}

func TestPlayerDataFilterFetchPreservesResponseMetadata(t *testing.T) {
	vm := playerFilterVM(t)
	runPlayerFilter(t, vm, `class Headers {constructor(){this.deleted=[];} delete(key){this.deleted.push(key);}} class Response {constructor(body,options={}){this.body=body;this.status=options.status||200;this.statusText=options.statusText||'OK';this.headers=options.headers||new Headers();this.ok=this.status<400;this.url='https://www.youtube.com/youtubei/v1/player';this.type='basic';this.redirected=true;} clone(){return new Response(this.body,this);} text(){return Promise.resolve(this.body);}} const original=new Response('{"videoDetails":{"videoId":"target"},"adPlacements":[1]}'); window.fetch=()=>Promise.resolve(original);`)
	runPlayerFilter(t, vm, browserAdFilterScript)
	runPlayerFilter(t, vm, `let result; fetch('/youtubei/v1/player').then(r=>{result=JSON.stringify([r.body,r.status,r.url,r.type,r.redirected,r.headers.deleted]);});`)
	got := runPlayerFilter(t, vm, "result")
	if got.String() != `["{\"videoDetails\":{\"videoId\":\"target\"}}",200,"https://www.youtube.com/youtubei/v1/player","basic",true,["content-length","content-encoding"]]` {
		t.Fatalf("filtered fetch metadata: %s", got)
	}
	runPlayerFilter(t, vm, `let untouched; fetch('/not-player').then(r=>untouched=r===original);`)
	if !runPlayerFilter(t, vm, "untouched").ToBoolean() {
		t.Fatal("unrelated fetch response was replaced")
	}
	runPlayerFilter(t, vm, `original.body='not json'; let malformed; fetch('/youtubei/v1/player').then(r=>malformed=r===original);`)
	if !runPlayerFilter(t, vm, "malformed").ToBoolean() {
		t.Fatal("non-JSON response was replaced")
	}
}
