package plugin

import (
	"strings"
	"testing"

	"github.com/cicord/cicord/internal/discord"
)

// SEC-P-03: staging is a step of a send. A plugin without a send grant cannot
// stage host memory for a channel message; an interaction reply (no grant
// needed) still carries files.
func TestUploadStagingRequiresTheSendItServes(t *testing.T) {
	src := `import {command,definePlugin} from "@cicord/sdk";
 export default definePlugin({commands:{
  post:command({description:"post",async run(ctx,it){
   try{await ctx.server.channel({id:"100"}).send({files:[{name:"a.bin",data:new Uint8Array(1024)}]});await it.reply("sent")}
   catch(e){await it.reply(e.name+": "+e.message)}}}),
  card:command({description:"card",async run(ctx,it){await it.reply({files:[{name:"a.bin",data:new Uint8Array(70000)}]})}})}});`
	h := start(t, src, func(e *Env) { e.Grants = []string{capChannelsRead} })
	if err := h.command("post", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if got := contentOf(h.responses()[0]); !strings.Contains(got, "CapabilityError") || !strings.Contains(got, capMessagesSend) {
		t.Fatalf("ungranted staging: %q", got)
	}
	if n := h.inst.env.quota.uploads.Load(); n != 0 {
		t.Fatalf("%d bytes stayed reserved", n)
	}
	resetCalls(h)
	if err := h.command("card", nil, discord.Resolved{}); err != nil {
		t.Fatal(err)
	}
	if files := h.responses()[0].Data.Files; len(files) != 1 || len(files[0].Data) != 70000 {
		t.Fatalf("interaction reply files: %v", files)
	}
	if n := h.inst.env.quota.uploads.Load(); n != 0 || stagedProcess.Load() != 0 {
		t.Fatalf("reservation not released after the send: %d, process %d", n, stagedProcess.Load())
	}
	if _, err := asyncOps["upload.part"](h.inst, nil, []byte(`{"handle":0,"size":10,"data":"","op":"server.ban"}`)); err == nil {
		t.Fatal("staged a file for an operation that sends no message")
	}
}

// m4: a declared size reserves budget, not memory, and the installation budget
// is shared with private VMs and bounded per process.
func TestUploadReservationsAreBudgetsNotBuffers(t *testing.T) {
	q := newInstallationQuota()
	if !reserveStaged(&q.uploads, maxStagedInstallation) || reserveStaged(&q.uploads, 1) {
		t.Fatal("installation budget not enforced")
	}
	q.uploads.Add(-maxStagedInstallation)
	stagedProcess.Add(-maxStagedInstallation)
	other := newInstallationQuota()
	stagedProcess.Add(maxStagedProcess - 10)
	if reserveStaged(&other.uploads, 11) || other.uploads.Load() != 0 {
		t.Fatal("process budget not enforced")
	}
	stagedProcess.Add(-(maxStagedProcess - 10))

	h := start(t, `import {definePlugin} from "@cicord/sdk"; export default definePlugin({});`, nil)
	i := h.inst
	i.grants[capMessagesSend] = true
	inv := i.bg
	work, err := opUploadPart(i, inv, []byte(`{"handle":0,"size":8388608,"data":"AAAA","op":"channel.send"}`))
	if err != nil {
		t.Fatal(err)
	}
	id, err := work(inv.ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := i.uploads.pending[id.(int64)]
	if cap(f.data) > 1<<10 || i.env.quota.uploads.Load() != 8<<20 {
		t.Fatalf("first chunk allocated %d bytes, reserved %d", cap(f.data), i.env.quota.uploads.Load())
	}
	vm, err := i.privateVM(t.Context(), CallFrame{})
	if err != nil {
		t.Fatal(err)
	}
	defer vm.shutdown()
	if vm.env.quota != i.env.quota {
		t.Fatal("a private VM has its own host budget")
	}
	i.freeUpload(inv, id.(int64))
	if i.env.quota.uploads.Load() != 0 || stagedProcess.Load() != 0 {
		t.Fatal("free did not release the reservation")
	}
}
