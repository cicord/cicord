package plugin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cicord/cicord/internal/build"
	"github.com/cicord/cicord/internal/discord"
	"github.com/cicord/cicord/internal/media"
)

type mediaTransport struct {
	fn func(*http.Request) *http.Response
}

func (m mediaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return m.fn(r), nil }

func TestPluginMediaContract(t *testing.T) {
	raw := []byte{0, 255, 1, 42}
	var body []byte
	src := `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{verify:command({description:"media",options:{attachment:{type:"attachment",description:"input"}},
 async run(ctx,it){
  const file=await ctx.assets.read("logo.bin");const response=await ctx.http.fetch("https://files.example.org/bin");
  const att=await it.options.attachment.bytes();const fetched=await response.bytes();
  if (file.toHex()!==att.toHex()||file.toHex()!==fetched.toHex())throw Error("binary mismatch");
  const text="Welcome 🎉";if (new TextDecoder().decode(new TextEncoder().encode(text))!==text)throw Error("bad UTF-8");
  const card=await ctx.render.image({width:120,height:45,background:"#112233",layers:[{type:"text",x:2,y:2,text,size:18,color:"#ffffff"}]});
  await it.reply({files:[{name:"card.png",...card,description:"welcome"},{name:"input.bin",data:file}],embeds:[{image:{url:"attachment://card.png"}}]});
 }}),hash:command({description:"hash",async run(ctx,it){const h=await ctx.media.hash("https://files.example.org/image",{algo:"phash"});await it.reply(h+" "+ctx.media.distance(h,h));}})}});`
	pack, _ := json.Marshal(map[string][]byte{"logo.bin": raw})
	code := bundleSource(t, src) + build.AssetMarker + string(pack) + "\n"
	h := startCode(t, code, func(e *Env) {
		e.Grants = []string{"render", "media.analyze", "discord.messages.read", "http.fetch"}
		e.NetworkAllow = []string{"files.example.org"}
		e.HTTPClient = &http.Client{Transport: mediaTransport{func(r *http.Request) *http.Response {
			data := raw
			if r.URL.Path == "/image" {
				data = body
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}
		}}}
	})
	res := discord.Resolved{Attachments: map[string]discord.Attachment{"12": {ID: "12", URL: discord.CDNBase + "/attachments/12/raw.bin", Filename: "raw.bin"}}}
	if err := h.command("verify", []discord.InteractionOption{{Name: "attachment", Type: discord.OptionAttachment, Value: "12"}}, res); err != nil {
		t.Fatal(err)
	}
	reply := h.responses()[0].Data
	if len(reply.Files) != 2 || reply.Files[0].Description != "welcome" || !bytes.Equal(reply.Files[1].Data, raw) || reply.Embeds[0].ImageURL != "attachment://card.png" {
		t.Fatalf("unexpected file reply: %v", reply)
	}
	body = reply.Files[0].Data
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := h.command("hash", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	text := contentOf(h.responses()[1])
	if len(text) != 18 || !strings.HasSuffix(text, " 0") {
		t.Fatalf("hash/distance: %s", text)
	}
}

func TestLargeUploadChunks(t *testing.T) {
	h := start(t, `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{upload:command({description:"upload",async run(ctx,it){const data=new Uint8Array(2*1024*1024);data[data.length-1]=42;await it.reply({files:[{name:"large.bin",data}]})}})}});`, nil)
	if err := h.command("upload", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	files := h.responses()[0].Data.Files
	if len(files) != 1 || len(files[0].Data) != 2<<20 || files[0].Data[len(files[0].Data)-1] != 42 {
		t.Fatal("large file was truncated")
	}
}
func TestMediaBoundary(t *testing.T) {
	i := &Instance{egress: newEgress([]string{"example.org"}, false, nil, 100)}
	for _, u := range []string{"https://other.example/raw", "https://cdn.discordapp.com:9999/file", "http://cdn.discordapp.com/file", "https://name@cdn.discordapp.com/file"} {
		if _, err := i.downloadMedia(context.Background(), u, true); err == nil {
			t.Fatalf("unexpected CDN access: %q", u)
		}
	}
	for _, op := range []string{"render.image", "media.hash", "attachment.bytes"} {
		if _, err := asyncOps[op](i, nil, json.RawMessage("{}")); err == nil {
			t.Errorf("ungranted %s succeeded", op)
		}
	}
	if _, err := opMediaDistance(i, nil, nil); err == nil {
		t.Error("ungranted media distance")
	}
	if _, err := i.readAsset("../data"); err == nil {
		t.Fatal("unknown asset")
	}
	bogus := build.AssetMarker + `{"../file":"` + base64.StdEncoding.EncodeToString([]byte("abc")) + `"}`
	if _, _, err := unpackAssets(bogus); err == nil {
		t.Fatal("invalid package path")
	}
}

func TestAttachmentReadAtLimit(t *testing.T) {
	const n = 8 << 20
	body := bytes.Repeat([]byte{255}, n)
	src := `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{read:command({description:"read",options:{file:{type:"attachment",description:"file"}},async run(ctx,it){
  try{const data=await it.options.file.bytes();await it.reply(data.length+" "+data[data.length-1])}catch(e){await it.reply(e.message)}
 }})}});`
	h := start(t, src, func(e *Env) {
		e.HTTPClient = &http.Client{Transport: mediaTransport{func(r *http.Request) *http.Response {
			b := io.Reader(bytes.NewReader(body))
			if r.URL.Path == "/too-big" {
				b = io.MultiReader(b, strings.NewReader("!"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(b), Header: http.Header{}}
		}}}
	})
	for _, tc := range []struct{ path, want string }{{"/limit", "8388608 255"}, {"/too-big", "larger than 8388608 bytes"}} {
		resetCalls(h)
		r := discord.Resolved{Attachments: map[string]discord.Attachment{"42": {URL: discord.CDNBase + tc.path}}}
		if err := h.command("read", []discord.InteractionOption{{Name: "file", Type: discord.OptionAttachment, Value: "42"}}, r); err != nil {
			t.Fatal(err)
		}
		if got := contentOf(h.responses()[0]); !strings.Contains(got, tc.want) {
			t.Fatalf("read %s: %s", tc.path, got)
		}
	}
}

// Downloads wait on the network without a process-wide worker slot (review MJ2,
// SEC-B-05): with every slot taken, attachment.bytes completes and media.hash
// still reaches its host; the hash then waits for a slot for the decode only.
func TestMediaDownloadsDoNotHoldWorkerSlots(t *testing.T) {
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewGray(image.Rect(0, 0, 8, 8)))
	requested := make(chan string, 4)
	src := `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{
  read:command({description:"read",options:{file:{type:"attachment",description:"file"}},async run(ctx,it){await it.reply(String((await it.options.file.bytes()).length))}}),
  hash:command({description:"hash",async run(ctx,it){await it.reply(await ctx.media.hash("https://files.example.org/image",{algo:"dhash"}))}})}});`
	h := start(t, src, func(e *Env) {
		e.Grants = []string{"media.analyze", "discord.messages.read"}
		e.NetworkAllow = []string{"files.example.org"}
		e.HTTPClient = &http.Client{Transport: mediaTransport{func(r *http.Request) *http.Response {
			requested <- r.URL.Path
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(img.Bytes())), Header: http.Header{}}
		}}}
	})
	var held []func()
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		release, err := media.Acquire(ctx)
		cancel()
		if err != nil {
			break
		}
		held = append(held, release)
	}
	releaseAll := func() {
		for _, r := range held {
			r()
		}
		held = nil
	}
	defer releaseAll()
	r := discord.Resolved{Attachments: map[string]discord.Attachment{"42": {URL: discord.CDNBase + "/a.png"}}}
	if err := h.command("read", []discord.InteractionOption{{Name: "file", Type: discord.OptionAttachment, Value: "42"}}, r); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); got != strconv.Itoa(img.Len()) {
		t.Fatalf("attachment.bytes with every worker slot taken: %q", got)
	}
	<-requested
	done := make(chan error, 1)
	go func() { done <- h.command("hash", nil, discord.Resolved{}) }()
	select {
	case p := <-requested:
		if p != "/image" {
			t.Fatal(p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("media.hash did not start its download while the worker slots were taken")
	}
	releaseAll()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[1]); len(got) != 16 {
		t.Fatalf("hash: %q", got)
	}
}
