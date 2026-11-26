// Cicord SDK prelude: implements @cicord/sdk (spec/sdk/cicord-sdk.d.ts) inside the plugin VM.
//
// Evaluated once per VM (QuickJS-ng, internal/plugin/qjs) as `(function (host) { ... })`.
// `host` is the Go side; it never leaks into plugin code. Data crosses the boundary as
// strings (JSON for structured data):
//   host.raw(kind, a, b, n)  -> string | undefined | { e: errorJSON }   (see hraw below)
//   host.parseJSON(json)     -> value  (host-produced JSON, parsed outside the sync budget)
//   host.onRejection(fn)     -> registers the unhandled-rejection tracker
// Go calls the functions of the returned object (string arguments) and settles async host
// calls with settle(). Every capability check happens in Go.
(function (host) {
  "use strict";

  const freeze = Object.freeze;
  const hasOwn = (o, k) => o != null && Object.prototype.hasOwnProperty.call(o, k);

  // ─────────────────────────────────────────────────────────── errors

  class CapabilityError extends Error {
    constructor(capability, message) {
      super(message || `missing capability "${capability}": grant it to this plugin instance in bot.yaml`);
      Object.defineProperty(this, "capability", { value: capability, enumerable: true });
    }
  }
  Object.defineProperty(CapabilityError.prototype, "name", { value: "CapabilityError", writable: true, configurable: true });

  function mkError(name, message, capability) {
    if (capability) return new CapabilityError(capability, message);
    const e = name === "TypeError" ? new TypeError(message) : new Error(message);
    if (name && name !== "Error" && name !== "TypeError") {
      Object.defineProperty(e, "name", { value: name, writable: true, configurable: true });
    }
    return e;
  }

  // Error descriptions and log lines are cut here, inside the VM, so that a plugin cannot
  // push large strings onto the host (which also enforces caps).
  const MAX_ERROR_TEXT = 16384, MAX_LOG_MESSAGE = 4096, MAX_LOG_FIELDS = 16384;
  const cut = (s, n) => (s.length > n ? s.slice(0, n) + "…" : s);

  function errInfo(e) {
    try {
      if (e instanceof Error) {
        return JSON.stringify({
          name: cut(String(e.name || "Error"), 200),
          message: cut(String(e.message), MAX_ERROR_TEXT),
          stack: typeof e.stack === "string" ? cut(e.stack, MAX_ERROR_TEXT) : "",
          capability: e instanceof CapabilityError ? cut(String(e.capability), 200) : undefined,
        });
      }
      return JSON.stringify({ name: "Error", message: "non-Error value thrown: " + cut(safeString(e), MAX_ERROR_TEXT) });
    } catch (_) {
      return JSON.stringify({ name: "Error", message: "unserializable error" });
    }
  }

  function safeString(v) {
    try {
      if (typeof v === "string") return v;
      const s = JSON.stringify(v);
      return s === undefined ? String(v) : s;
    } catch (_) {
      try { return String(v); } catch (_) { return "[unprintable]"; }
    }
  }

  // ─────────────────────────────────────────────────────────── host calls

  // Host call kinds (internal/plugin/vm.go).
  const K_CALL = 1, K_ASYNC = 2, K_LOG = 3, K_SET_TIMER = 4, K_CLEAR_TIMER = 5, K_DONE = 6;

  function mkErrorJSON(json) {
    let d;
    try { d = JSON.parse(json); } catch (_) { d = { message: String(json) }; }
    return mkError(d.name, d.message, d.capability);
  }

  const raw = host.raw;
  function hraw(kind, a, b, n) {
    const r = raw(kind, a, b, n === undefined ? 0 : n);
    if (r !== undefined && typeof r !== "string") throw mkErrorJSON(r.e);
    return r;
  }

  // Host-produced JSON is parsed by the host, outside the sync budget.
  const jparse = (s) => host.parseJSON(s);
  const parse = (r) => (r === undefined || r === null || r === "" ? undefined : jparse(r));
  const enc = (args) => JSON.stringify(args === undefined ? null : args);
  const hcall = (op, args) => parse(hraw(K_CALL, op, enc(args)));

  // Async host calls: Go returns an id and later calls settle(id, status, data).
  const pendingCalls = new Map();
  async function acall(op, args) {
    const id = Number(hraw(K_ASYNC, op, enc(args)));
    const r = await new Promise((resolve, reject) => { pendingCalls.set(id, [resolve, reject]); });
    return parse(r);
  }
  function settle(id, status, data) {
    const n = Number(id);
    const p = pendingCalls.get(n);
    if (!p) return;
    pendingCalls.delete(n);
    if (status === "err") p[1](mkErrorJSON(data));
    else p[0](status === "ok" ? data : undefined);
  }

  // Unhandled rejections: recorded here, collected by Go at the end of each turn.
  const rejections = new Map();
  host.onRejection((p, reason, handled) => {
    if (handled) rejections.delete(p);
    else rejections.set(p, reason);
    return rejections.size > 0;
  });
  function takeRejections() {
    const out = [];
    for (const r of rejections.values()) {
      if (out.length === 20) break;
      out.push(errInfo(r));
    }
    rejections.clear();
    return JSON.stringify(out);
  }

  // allowedMentions.users / .roles: a boolean, or a list of ids (strings) or of objects
  // with an id (users, members, roles), sent to the host as ids.
  function mentionIds(v, name) {
    if (v === undefined || v === null || typeof v === "boolean") return v;
    if (!Array.isArray(v)) throw new TypeError("allowedMentions." + name + " must be a boolean or an array of ids");
    return v.map((x) => (x !== null && typeof x === "object" ? x.id : x));
  }
  function withMentions(m) {
    const am = m.allowedMentions;
    if (am === undefined || am === null || typeof am !== "object") return m;
    return Object.assign({}, m, {
      allowedMentions: Object.assign({}, am, { users: mentionIds(am.users, "users"), roles: mentionIds(am.roles, "roles") }),
    });
  }

  function msgIn(m) {
    if (typeof m === "string") return { content: m };
    if (m === null || typeof m !== "object") throw new TypeError("message must be a string or a message payload");
    return withMentions(m);
  }

  // ─────────────────────────────────────────────────────────── entities

  function mkUser(d) {
    if (!d) return undefined;
    return freeze({
      id: d.id,
      username: d.username || "",
      displayName: d.displayName || d.username || "",
      avatarUrl: d.avatarUrl || undefined,
      createdAt: d.createdAt ? new Date(d.createdAt) : new Date(NaN),
      flags: freeze(d.flags || []),
      bot: !!d.bot,
      mention: "<@" + d.id + ">",
    });
  }

  function mkInvite(d) {
    return freeze(Object.assign({},d,{inviter:d.inviter?mkUser(d.inviter):undefined,expiresAt:d.expiresAt?new Date(d.expiresAt):undefined}));
  }
  function mkAudit(a) {
    return freeze(Object.assign({},a,{at:new Date(a.at),actor:a.actor?mkUser(a.actor):undefined}));
  }
  const RoleProto = {
    async members(options) {
      const o = options || {};
      const list = await acall("role.members", { role: { id: this.id }, limit: o.limit, after: o.after === undefined ? undefined : String(o.after) });
      return freeze(list.map(mkMember));
    },
  };
  freeze(RoleProto);

  function mkRole(d) {
    if (!d) return undefined;
    return freeze(Object.assign(Object.create(RoleProto), {
      id: d.id, key: d.key || undefined, name: d.name || "", color: d.color || undefined, mention: d.mention,
      // Unknown (role not cached): left undefined, never guessed.
      position: typeof d.position === "number" ? d.position : undefined,
      managed:d.managed, declaredMembers:d.declaredMembers,
      mentionable: typeof d.mentionable === "boolean" ? d.mentionable : undefined,
    }));
  }

  function mkAttachment(d) {
    return freeze({ id: d.id, filename: d.filename, url: d.url, contentType: d.contentType || undefined, size: d.size });
  }

  const TextChannelProto = {
    async pins(){return (await acall("channel.pins",{channelId:this.id})).map(mkMessage);},
    async typing(){await acall("channel.typing",{channelId:this.id});},
    async sendAs(identity,message){return mkMessage(await acall("channel.sendAs",{channelId:this.id,identity,message:msgIn(message)}));},
    async send(message) {
      return mkMessage(await acall("channel.send", { channelId: this.id, message: msgIn(message) }));
    },
    async bulkDelete(ids, reason) {
      return acall("channel.bulkDelete", {channelId:this.id,ids,reason});
    },
    async previewPurge(filter) {
      return acall("channel.previewPurge", Object.assign({},filter,{channelId:this.id}));
    },
    async purge(filter) {
      return acall("channel.purge", Object.assign({},filter,{channelId:this.id}));
    },
    async messages(options) {
      const o = options || {};
      const list = await acall("channel.messages", { channelId: this.id, limit: o.limit, before: o.before, after: o.after });
      return list.map(mkMessage);
    },
    async message(id) {
      return mkMessage(await acall("channel.message", { channelId: this.id, messageId: String(id) }));
    },
    async createThread(name, options) {
      const o = options || {};
      return mkChannel(await acall("channel.createThread", { channelId: this.id, name, private: !!o.private, fromMessage: o.fromMessage }));
    },
    toString() { return this.mention; },
  };

  function mkChannel(d) {
    if (!d) return undefined;
    const c = Object.create(TextChannelProto);
    c.id = d.id;
    c.key = d.key || undefined;
    c.name = d.name || "";
    c.type = d.type || "text";
    c.categoryId = d.categoryId || undefined;
    c.mention = "<#" + d.id + ">";
    return freeze(c);
  }

  const MessageProto = {
    async pin(){await acall("message.pin",{channelId:this.channel.id,messageId:this.id});},
    async unpin(){await acall("message.unpin",{channelId:this.channel.id,messageId:this.id});},
    async crosspost(){await acall("message.crosspost",{channelId:this.channel.id,messageId:this.id});},
    async reactionUsers(emoji,options={}) {return (await acall("message.reactionUsers",{channelId:this.channel.id,messageId:this.id,emoji,limit:options.limit,after:options.after})).map(mkUser);},
    async removeReaction(emoji,userId){await acall("message.removeReaction",{channelId:this.channel.id,messageId:this.id,emoji,userId});},
    async reply(message) {
      return mkMessage(await acall("message.reply", { channelId: this.channel.id, messageId: this.id, message: msgIn(message) }));
    },
    async edit(message) {
      return mkMessage(await acall("message.edit", { channelId: this.channel.id, messageId: this.id, authorId: this.author && this.author.id, message: msgIn(message) }));
    },
    async delete(reason) {
      await acall("message.delete", { channelId: this.channel.id, messageId: this.id, reason });
    },
    async react(emoji) {
      await acall("message.react", { channelId: this.channel.id, messageId: this.id, emoji: String(emoji) });
    },
  };

  function mkMessage(d) {
    if (!d) return undefined;
    const m = Object.create(MessageProto);
    m.id = d.id;
    m.url=d.url; m.reference=d.reference ? freeze(d.reference) : undefined; m.reactions=freeze((d.reactions||[]).map(r=>freeze(r)));
    m.content = d.content || "";
    m.author = mkUser(d.author);
    m.member = d.member ? mkMember(d.member) : undefined;
    m.channel = mkChannel(d.channel);
    m.attachments = freeze((d.attachments || []).map(mkAttachment));
    m.embeds = freeze((d.embeds || []).map((e) => freeze(e)));
    m.createdAt = d.createdAt ? new Date(d.createdAt) : new Date(NaN);
    return freeze(m);
  }

  // A ResourceRef, or an object with an id (a Channel, a Role…), as the host reads it.
  const refIn = (ref) => (ref !== null && typeof ref === "object" ? { id: ref.id } : ref);

  const memberRoles = new WeakMap();
  const rolesOf = (m) => memberRoles.get(m) || [];

  const MemberProto = {
    permissionsIn(channel) {
      return freeze(hcall("member.permissionsIn", {userId:this.id,ref:refIn(channel)}) || []);
    },
    canView(channel) {
      const p=this.permissionsIn(channel);
      return p.includes("ViewChannel") && p.includes("ReadMessageHistory");
    },
    get roles() { return freeze(rolesOf(this).slice()); },
    hasRole(role) {
      const id = hcall("resolve.role", { ref: role });
      return rolesOf(this).some((r) => r.id === id);
    },
    async setRoles(roles, reason) {
      if (!Array.isArray(roles)) throw new TypeError("setRoles(): expected roles");
      const updated = await acall("member.setRoles", { userId: this.id, roles: roles.map(refIn), reason });
      memberRoles.set(this, updated.map(mkRole));
    },
    async addRole(role, reason) {
      const r = mkRole(await acall("member.addRole", { userId: this.id, role, reason }));
      const list = memberRoles.get(this);
      if (list && !list.some((x) => x.id === r.id)) list.push(r);
    },
    async removeRole(role, reason) {
      const r = await acall("member.removeRole", { userId: this.id, role, reason });
      const list = memberRoles.get(this);
      if (list) memberRoles.set(this, list.filter((x) => x.id !== r.id));
    },
    async setNickname(nickname, reason) {
      await acall("member.setNickname", { userId: this.id, nickname: nickname === null || nickname === undefined ? "" : String(nickname), reason });
    },
    async removeTimeout(reason) { await acall("member.timeout", { userId: this.id, durationMs: 0, reason }); },
    async timeout(durationMs, reason) {
      await acall("member.timeout", { userId: this.id, durationMs: Number(durationMs), reason });
    },
    async kick(reason) {
      await acall("member.kick", { userId: this.id, reason });
    },
    async ban(options) {
      const o = options || {};
      await acall("member.ban", { userId: this.id, reason: o.reason, deleteMessagesSeconds: o.deleteMessagesSeconds });
    },
    async send(message,options) {
      return mkMessage(await acall("member.send", { userId: this.id, message: msgIn(message),retractable:options && options.retractable }));
    },
    async moveTo(channel, reason) {
      if (channel === null || channel === undefined) throw new TypeError("moveTo: a voice channel is required (use disconnect() to disconnect)");
      await acall("member.moveTo", { userId: this.id, channel: refIn(channel), reason });
    },
    async disconnect(reason) {
      await acall("member.moveTo", { userId: this.id, disconnect: true, reason });
    },
    toString() { return this.mention; },
  };

  function mkMember(d) {
    if (!d) return undefined;
    const m = Object.create(MemberProto);
    m.declaredRoles=freeze((d.declaredRoles||[]).slice());
    m.user = mkUser(d.user);
    m.id = d.user.id;
    m.nickname = d.nick || undefined;
    m.displayName = d.displayName || m.user.displayName;
    m.mention = "<@" + d.user.id + ">";
    m.pending = d.pending;
    m.flags = freeze(d.flags || []);
    m.premiumSince = d.premiumSince ? new Date(d.premiumSince) : undefined;
    m.timedOutUntil = d.timedOutUntil ? new Date(d.timedOutUntil) : undefined;
    m.avatarUrl = d.avatarUrl || undefined;
    m.joinedAt = d.joinedAt ? new Date(d.joinedAt) : new Date(NaN);
    memberRoles.set(m, (d.roles || []).map(mkRole));
    m.permissions = Array.isArray(d.permissions) ? freeze(d.permissions.slice()) : undefined;
    m.access = d.access || undefined;
    return freeze(m);
  }

  // ─────────────────────────────────────────────────────────── interactions

  const handles = new WeakMap();
  const hOf = (it) => handles.get(it);

  function replyIn(m) {
    if (typeof m === "string") return { content: m };
    if (m === null || typeof m !== "object") throw new TypeError("reply must be a string or a message payload");
    return withMentions(m);
  }

  const InteractionProto = {
    get replied() { return hcall("it.state", { h: hOf(this) }).replied; },
    get deferred() { return hcall("it.state", { h: hOf(this) }).deferred; },
    async reply(message) { await acall("it.reply", { h: hOf(this), message: replyIn(message) }); },
    async defer(options) { await acall("it.defer", { h: hOf(this), ephemeral: options && options.ephemeral }); },
    async followUp(message) { return mkMessage(await acall("it.followUp", { h: hOf(this), message: replyIn(message) })); },
    async showModal(modal) { await acall("it.showModal", { h: hOf(this), modal }); },
  };
  const ComponentProto = Object.create(InteractionProto);
  ComponentProto.update = async function (message) {
    await acall("it.update", { h: hOf(this), message: msgIn(message) });
  };

  function baseInteraction(proto, d) {
    const o = Object.create(proto);
    o.id = d.id;
    o.locale = d.locale || undefined;
    o.user = mkUser(d.user);
    o.member = d.member ? mkMember(d.member) : undefined;
    o.channel = d.channel ? mkChannel(d.channel) : undefined;
    handles.set(o, d.handle);
    return o;
  }

  function mkOptions(list) {
    const out = {};
    for (const o of list || []) {
      switch (o.type) {
        case "user": out[o.name] = mkUser(o.user); break;
        case "role": out[o.name] = mkRole(o.role); break;
        case "channel": out[o.name] = mkChannel(o.channel); break;
        case "mentionable": out[o.name] = o.user ? mkUser(o.user) : mkRole(o.role); break;
        case "attachment": out[o.name] = o.attachment ? mkAttachment(o.attachment) : undefined; break;
        default: out[o.name] = o.value;
      }
    }
    return freeze(out);
  }

  function mkCommandInteraction(d) {
    const o = baseInteraction(InteractionProto, d);
    o.command = d.command;
    o.group = d.group || undefined;
    o.subcommand = d.subcommand || undefined;
    o.options = mkOptions(d.options);
    return freeze(o);
  }

  // Autocomplete interactions cannot reply: the handler returns the choices.
  function mkAutocompleteInteraction(d) {
    return freeze({
      id: d.id,
      locale: d.locale || undefined,
      user: mkUser(d.user),
      member: d.member ? mkMember(d.member) : undefined,
      channel: d.channel ? mkChannel(d.channel) : undefined,
      command: d.command,
      group: d.group || undefined,
      subcommand: d.subcommand || undefined,
      options: freeze(Object.assign({}, d.options || {})),
    });
  }

  function mkComponentInteraction(d) {
    const o = baseInteraction(ComponentProto, d);
    o.componentId = d.componentId;
    o.values = freeze((d.values || []).slice());
    o.message = d.message ? mkMessage(d.message) : undefined;
    return freeze(o);
  }

  function mkModalInteraction(d) {
    const o = baseInteraction(InteractionProto, d);
    o.modalId = d.modalId;
    o.selections=deepFreeze(Object.assign({},d.selections||{}));
    o.fields = freeze(Object.assign({}, d.fields || {}));
    return freeze(o);
  }

  // ─────────────────────────────────────────────────────────── context

  function deepFreeze(v) {
    if (v && typeof v === "object" && !Object.isFrozen(v)) {
      Object.freeze(v);
      for (const k of Object.keys(v)) deepFreeze(v[k]);
    }
    return v;
  }

  const LEVELS = { debug: 0, info: 1, warn: 2, error: 3 };
  const hlog = (level, message, fields) => {
    const f = fields && fields.length > MAX_LOG_FIELDS ? JSON.stringify({ fields: `[${fields.length} bytes: dropped]` }) : fields;
    return hraw(K_LOG, cut(message, MAX_LOG_MESSAGE), f, LEVELS[level]);
  };

  function logFn(level) {
    return (message, fields) => {
      let f = "";
      if (fields !== undefined) {
        try { f = JSON.stringify(fields); } catch (_) { f = JSON.stringify({ fields: "[unserializable]" }); }
      }
      hlog(level, String(message), f || "");
    };
  }

  function mkContext(d) {
    const server = freeze({
      overlays: freeze({
        async apply(overlay) { await acall("server.overlayApply",overlay); },
        async remove(id) { await acall("server.overlayRemove",{id}); },
        async list() { return freeze((await acall("server.overlays",{})).map(o=>deepFreeze({...o,until:new Date(o.until)}))); },
      }),
      async incident(actions) { await acall("server.incident",actions); },
      async requestSync(reason) { await acall("server.requestSync",{reason}); },
      id: d.server.id,
      key: d.server.key,
      name: d.server.name,
      iconUrl: d.server.iconUrl || undefined,
      preferredLocale: d.server.preferredLocale || undefined,
      async members(options = {}) { return freeze((await acall("server.members",options)).map(mkMember)); },
      audience(channel) { return freeze(hcall("server.audience",{ref:refIn(channel)})); },
      compareAudience(source,destination) { return hcall("server.compareAudience",{source:refIn(source),destination:refIn(destination)}); },
      async retractDM(messageId){await acall("dm.retract",{messageId});},
      async ban(userId, options = {}) {
        await acall("member.ban", {userId: String(userId), reason: options.reason, deleteMessagesSeconds: options.deleteMessagesSeconds});
      },
      async unban(userId, reason) { await acall("server.unban", {userId: String(userId), reason}); },
      async invites() { return freeze((await acall("server.invites",{})).map(mkInvite)); },
      async auditLog(options = {}) {
        return freeze((await acall("server.auditLog",options)).map(mkAudit));
      },
      async bans(options = {}) {
        const entries=await acall("server.bans", {limit: options.limit, after: options.after});
        return freeze(entries.map(b => freeze({user: mkUser(b.user), reason: b.reason})));
      },
      get afkChannelId() {return hcall("server.afkChannel",{}) || undefined;},
      get ownerId() { return hcall("server.owner", {}) || undefined; },
      channel(ref) { return mkChannel(hcall("server.channel", { ref })); },
      role(ref) { return mkRole(hcall("server.role", { ref })); },
      async member(userId) {
        const m = await acall("server.member", { userId: String(userId) });
        return m ? mkMember(m) : undefined;
      },
      async createChannel(options) {
        if (!options || typeof options !== "object") throw new TypeError("createChannel: options are required");
        return mkChannel(await acall("server.createChannel", options));
      },
      async deleteChannel(ref, reason) {
        await acall("server.deleteChannel", { ref: refIn(ref), reason });
      },
      async editChannel(ref, options) {
        if (!options || typeof options !== "object") throw new TypeError("editChannel: options are required");
        return mkChannel(await acall("server.editChannel", Object.assign({}, options, { ref: refIn(ref) })));
      },
      async voiceMembers(ref) {
        const list = await acall("server.voiceMembers", { ref: refIn(ref) });
        return freeze(list.map(mkMember));
      },
    });

    const kv = freeze({
      async get(key) { return acall("kv.get", { key }); },
      async getEntry(key) { return (await acall("kv.entries", [key]))[0] ?? undefined; },
      async getMany(keys) { return (await acall("kv.entries", keys)).map(e => e === null ? undefined : e.value); },
      atomic() {
        const checks = [], ops = [];
        let used = false;
        const append = (list, value) => {
          if (used || checks.length + ops.length >= 128) throw new TypeError("kv.atomic: closed or full transaction");
          list.push(value); return builder;
        };
        const builder = freeze({
          check(key, version) { return append(checks, {key, version: version ?? null}); },
          set(key, value, options) {
            if (value === undefined) throw new TypeError("kv.atomic.set: expected JSON");
            return append(ops, {op:"set", key, value, ttlSeconds:options?.ttlSeconds});
          },
          increment(key, by=1) { if (!Number.isFinite(by)) throw new TypeError("expected finite delta"); return append(ops,{op:"increment",key,by}); },
          delete(key) { return append(ops,{op:"delete",key}); },
          rankSet(name,key,score) { if (!Number.isFinite(score)) throw new TypeError("expected finite score"); return append(ops,{op:"rankSet",name,key,score}); },
          rankRemove(name,key) { return append(ops,{op:"rankRemove",name,key}); },
          async commit() { if (used) throw new TypeError("transaction already committed"); used = true; return acall("kv.atomic",{checks,ops}); },
        });
        return builder;
      },
      ranking(name) {
        const run = (method, params) => acall("kv.rank", {name,method,...params});
        const write = (method, member, score) => { if (!Number.isFinite(score)) throw new TypeError("expected finite score"); return run(method,{member,score}); };
        return freeze({
          add: (member, delta) => write("add",member,delta), set: (member, score) => write("set",member,score),
          score: member => run("score",{member}), rank: member => run("rank",{member}),
          top: ({limit=100,offset=0}={}) => run("top",{limit,offset}),
          around: (member,radius) => run("around",{member,radius}), remove: member => run("remove",{member}), count: () => run("count",{}),
        });
      },
      async set(key, value, options) {
        if (value === undefined) throw new TypeError("kv.set: value must be JSON (got undefined)");
        await acall("kv.set", { key, value, ttlSeconds: options && options.ttlSeconds });
      },
      async delete(key) { await acall("kv.delete", { key }); },
      async increment(key, by) { return acall("kv.increment", { key, by: by === undefined ? 1 : by }); },
      async list(options) {
        const o = options || {};
        const r = await acall("kv.list", { prefix: o.prefix, limit: o.limit, cursor: o.cursor });
        return { items: r.items, cursor: r.cursor || undefined };
      },
    });

    const schedule = freeze({
      async after(delayMs, task, payload) { return freeze(await acall("schedule.after", { delayMs, task, payload })); },
      async cancel(id) { await acall("schedule.cancel", { id }); },
    });

    const secrets = freeze({
      has(name) { return hcall("secrets.has", {name}); },
      ref(name) {
        if (!this.has(name)) throw new Error("plugin secret is unavailable");
        return "cicord-secret:" + name;
      },
    });
    const files = freeze({
      list(glob) { return acall("files.list", {glob}); },
      read(path) { return acall("files.read", {path}); },
      async bytes(path) {
        const encoded = await acall("files.bytes", {path});
        const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        const out = new Uint8Array(Math.floor(encoded.replace(/=+$/, "").length * 3 / 4));
        let buffer = 0, bits = 0, pos = 0;
        for (let n=0; n<encoded.length && encoded[n]!=="="; n++) {
          buffer = (buffer << 6) | alphabet.indexOf(encoded[n]); bits += 6;
          if (bits >= 8) {bits -= 8; out[pos++] = buffer >> bits;}
        }
        return out;
      },
    });
    const http = freeze({
      async fetch(url, init) {
        const r = await acall("http.fetch", { url: String(url), init: init || {} });
        const body = r.body;
        return freeze({
          status: r.status,
          headers: freeze(r.headers),
          async text() { return body; },
          async json() { return jparse(body); },
        });
      },
    });

    const events = freeze({
      async publish(topic, payload) { await acall("events.publish", { topic, payload: payload === undefined ? null : payload }); },
    });

    const portal = freeze({
      async revoke(scope) { await acall("portal.revoke", scope); },
      async link(path, options) {
        const o = options || {};
        return acall("portal.link", { path, userId: o.scope ? (typeof o.user === "string" ? o.user : o.user?.id) : o.member?.id, scope: o.scope, expiresInSeconds: o.expiresInSeconds });
      },
    });

    return freeze({
      plugin: freeze(d.plugin),
      i18n: freeze({t(key,vars,options) {return hcall("i18n.t",{key,vars:vars||{},locale:options&&options.locale});}}),
      bot: freeze({ key: d.bot.key, user: mkUser(d.bot.user) }),
      server,
      config: deepFreeze(d.config || {}),
      log: freeze({ debug: logFn("debug"), info: logFn("info"), warn: logFn("warn"), error: logFn("error") }),
      kv,
      schedule,
      status: freeze({set(key,message) { hcall("status.set",{key,message}); }}),
      files,
      index(name, options = {}) {
        const language = options.language || "en";
        return freeze({
          upsert(id, document) { return acall("index.upsert", {name,language,id,document}); },
          remove(id) { return acall("index.remove", {name,language,id}); },
          clear() { return acall("index.clear", {name,language}); },
          search(query, options = {}) { return acall("index.search", {name,language,query,options}); },
        });
      },
      secrets,
      http,
      events,
      portal,
    });
  }

  // ─────────────────────────────────────────────────────────── definition

  const BRAND = Symbol("cicord.plugin");

  function definePlugin(definition) {
    if (definition === null || typeof definition !== "object") throw new TypeError("definePlugin: expected an object");
    Object.defineProperty(definition, BRAND, { value: true });
    return definition;
  }

  function command(definition) { return definition; }

  function subcommand(definition) { return definition; }

  function defineRoutes(routes) { return routes; }

  const sdk = freeze({ sdkFeatures:freeze({contextMenus:true}), definePlugin, command, subcommand, defineRoutes, CapabilityError });

  const entries = (o) => (o && typeof o === "object" ? Object.keys(o).map((k) => [k, o[k]]) : []);
  const fns = (o) => entries(o).map(([name, f]) => ({ name, type: typeof f }));

  // A command or subcommand: description, run and options. Option functions (autocomplete)
  // do not survive JSON: their typeof is reported instead.
  function describeLeaf(name, c) {
    return {
      name,
      description: c && c.description,
      run: typeof (c && c.run),
      optionsType: typeof (c && c.options),
      options: entries(c && c.options).map(([oname, o]) =>
        Object.assign({}, o, { name: oname, autocomplete: typeof (o && o.autocomplete) })),
    };
  }

  function describe() {
    const def = plugin;
    const sections = {};
    for (const k of ["events", "commands", "contextMenus","components", "modals", "schedules", "tasks", "subscriptions", "routes"]) {
      sections[k] = typeof def[k];
    }
    return JSON.stringify({
      keys: Object.keys(def),
      sections,
      onStart: typeof def.onStart,
      onStop: typeof def.onStop,
      events: fns(def.events),
      contextMenus: entries(def.contextMenus).map(([name,m])=>({name,type:m&&m.type,run:typeof(m&&m.run),permissions:m&&m.permissions,ephemeral:m&&m.ephemeral})),
      commands: entries(def.commands).map(([name, c]) => Object.assign(describeLeaf(name, c), {
        ephemeral: c && c.ephemeral,
        permissions: c && c.permissions,
        subcommandsType: typeof (c && c.subcommands),
        subcommands: entries(c && c.subcommands).map(([sname, sc]) => describeLeaf(sname, sc)),
        groupsType: typeof (c && c.groups),
        groups: entries(c && c.groups).map(([gname, g]) => ({
          name: gname,
          description: g && g.description,
          subcommandsType: typeof (g && g.subcommands),
          subcommands: entries(g && g.subcommands).map(([sname, sc]) => describeLeaf(sname, sc)),
        })),
      })),
      components: fns(def.components),
      modals: fns(def.modals),
      schedules: entries(def.schedules).map(([name, s]) => ({ name, cron: s && s.cron, type: typeof (s && s.run) })),
      tasks: fns(def.tasks),
      subscriptions: fns(def.subscriptions),
      routes: entries(def.routes).map(([name, r]) => ({ name, access: r && r.access, type: typeof (r && r.handler) })),
    });
  }

  let plugin = null;
  let ctx = null;

  // loadPlugin reads the bundle's exports (global `name`): "ok", "missing" or "nodefault".
  function loadPlugin(name) {
    const exportsObj = globalThis[name];
    if (exportsObj === undefined || exportsObj === null) return "missing";
    const def = typeof exportsObj === "object" && "default" in exportsObj ? exportsObj.default : undefined;
    if (!def || typeof def !== "object") return "nodefault";
    if (!def[BRAND]) throw new TypeError("the default export must be the result of definePlugin({...})");
    plugin = def;
    return "ok";
  }

  // ─────────────────────────────────────────────────────────── dispatch

  function setup(contextJSON) {
    ctx = mkContext(jparse(contextJSON));
  }

  function own(o, k) { return hasOwn(o, k) ? o[k] : undefined; }

  // commandTarget resolves a command path ("poll", "polls close", "polls admin purge") to
  // the command or subcommand definition.
  function commandTarget(path) {
    const parts = path.split(" ");
    const c = own(plugin.commands, parts[0]);
    if (!c || parts.length === 1) return c;
    const table = parts.length === 2 ? c.subcommands : ((g) => g && g.subcommands)(own(c.groups, parts[1]));
    return parts.length <= 3 ? own(table, parts[parts.length - 1]) : undefined;
  }

  function findHandler(kind, name) {
    switch (kind) {
      case "start": return plugin.onStart ? [plugin, plugin.onStart] : null;
      case "stop": return plugin.onStop ? [plugin, plugin.onStop] : null;
      case "event": { const f = own(plugin.events, name); return f ? [plugin.events, f] : null; }
      case "contextMenu": {const m=own(plugin.contextMenus,name);return m&&m.run?[m,m.run]:null;}
      case "command": { const c = commandTarget(name); return c && c.run ? [c, c.run] : null; }
      case "autocomplete": {
        // "<command path>/<option>"
        const i = name.lastIndexOf("/");
        const c = commandTarget(name.slice(0, i));
        const o = c && own(c.options, name.slice(i + 1));
        return o && typeof o.autocomplete === "function" ? [o, o.autocomplete] : null;
      }
      case "component":
      case "modal": {
        const table = kind === "component" ? plugin.components : plugin.modals;
        let f = own(table, name);
        if (!f) {
          const i = name.indexOf(":");
          if (i > 0) f = own(table, name.slice(0, i));
        }
        return f ? [table, f] : null;
      }
      case "schedule": { const s = own(plugin.schedules, name); return s && s.run ? [s, s.run] : null; }
      case "task": { const f = own(plugin.tasks, name); return f ? [plugin.tasks, f] : null; }
      case "subscription": { const f = own(plugin.subscriptions, name); return f ? [plugin.subscriptions, f] : null; }
      case "route": { const r = own(plugin.routes, name); return r && r.handler ? [r, r.handler] : null; }
    }
    return null;
  }

  function eventArgs(name, d) {
    switch (name) {
      case "channelCreate": case "channelUpdate": case "channelDelete":
        return [freeze(Object.assign({},d.structure,{channel:mkChannel(d.structure.channel),previous:d.structure.previous?mkChannel(d.structure.previous):undefined}))];
      case "roleCreate": case "roleUpdate": case "roleDelete":
        return [freeze(Object.assign({},d.structure,{role:mkRole(d.structure.role),previous:d.structure.previous?mkRole(d.structure.previous):undefined}))];
      case "threadCreate": case "threadDelete":
        return [freeze({thread:mkChannel(d.structure.thread),parent:d.structure.parent?mkChannel(d.structure.parent):undefined})];
      case "inviteCreate": case "inviteDelete": return [mkInvite(d.structure)];
      case "automodAction": case "serverUpdate": case "expressionsUpdate": case "webhooksUpdate": return [freeze(d.structure)];
      case "auditLogEntry": return [mkAudit(d.audit)];
      case "banAdd":
      case "banRemove": return [freeze({ user: mkUser(d.user) })];
      case "memberJoin": return [mkMember(d.member)];
      case "memberLeave": return [mkUser(d.user),mkMember(d.previous)];
      case "memberUpdate": return [mkMember(d.member), d.previous ? mkMember(d.previous) : undefined];
      case "directMessage":
      case "messageCreate":
      case "messageUpdate": return [mkMessage(d.message)];
      case "messageDelete": return [freeze({ id: d.id, channel: mkChannel(d.channel) })];
      case "reactionAdd":
      case "reactionRemove": return [freeze({ message: mkMessage(d.message), user: mkUser(d.user), emoji: d.emoji })];
      case "serverJoin": return [freeze({ id: d.server.id, name: d.server.name || "", iconUrl: d.server.iconUrl || undefined })];
      case "serverLeave": return [freeze({ id: d.server.id })];
      case "voiceStateUpdate": {
        const v = d.voice || {};
        return [freeze({
          user: mkUser(d.user),
          member: d.member ? mkMember(d.member) : undefined,
          channel: v.channel ? mkChannel(v.channel) : undefined,
          previous: v.previous ? mkChannel(v.previous) : undefined,
          selfMute: !!v.selfMute,
          selfDeaf: !!v.selfDeaf,
          streaming: !!v.streaming,
          video: !!v.video,
        })];
      }
    }
    return [];
  }

  function args(kind, name, d) {
    switch (kind) {
      case "event": return eventArgs(name, d);
      case "contextMenu": {const o=baseInteraction(InteractionProto,d);o.target=d.targetMember?mkMember(d.targetMember):mkMessage(d.targetMessage);return [freeze(o)];}
      case "command": return [mkCommandInteraction(d)];
      case "autocomplete": return [mkAutocompleteInteraction(d), freeze({ name: d.focused.name, value: d.focused.value })];
      case "component": return [mkComponentInteraction(d)];
      case "modal": return [mkModalInteraction(d)];
      case "task": return [d.payload, freeze(d.task)];
      case "subscription": return [d.payload];
      case "route": return [freeze({
        params: freeze(d.params || {}),
        query: freeze(d.query || {}),
        body: d.body,
        scope: d.scope ? freeze(d.scope) : undefined,
        viewer: d.viewer ? freeze({ user: mkUser(d.viewer.user), member: d.viewer.member ? mkMember(d.viewer.member) : undefined }) : undefined,
      })];
    }
    return [];
  }

  function routeOut(v) {
    if (v === undefined) return { status: 204 };
    if (v !== null && typeof v === "object" && !Array.isArray(v) && typeof v.status === "number" &&
        Object.keys(v).every((k) => k === "status" || k === "body" || k === "headers")) {
      return { status: v.status, body: v.body, headers: v.headers };
    }
    return { status: 200, body: v };
  }

  // Returns "0" when there is no handler. The handler's outcome is reported with
  // host.raw(K_DONE, "ok" | "fail", resultJSON | errorJSON, invocation id).
  function dispatch(inv, kind, name, dataJSON) {
    const h = findHandler(kind, name);
    if (!h) return "0";
    const id = Number(inv);
    const ok = (s) => hraw(K_DONE, "ok", s, id);
    const fail = (e) => hraw(K_DONE, "fail", errInfo(e), id);
    try {
      const d = dataJSON ? jparse(dataJSON) : {};
      const r = h[1].call(h[0], ctx, ...args(kind, name, d));
      Promise.resolve(r).then(
        (v) => {
          let s;
          try {
            if (kind === "route") s = JSON.stringify(routeOut(v));
            else if (kind === "autocomplete") s = JSON.stringify(v === undefined || v === null ? [] : v);
            ok(s);
          } catch (e) {
            fail(e);
          }
        },
        fail,
      );
    } catch (e) {
      fail(e);
    }
    return "1";
  }

  // ─────────────────────────────────────────────────────────── globals

  const g = globalThis;
  g.crypto = freeze({
    getRandomValues(view) {
      if (!(view instanceof Uint8Array)) throw new TypeError("crypto.getRandomValues expects a Uint8Array");
      const bytes = hcall("crypto.random",view.byteLength);
      for (let i=0;i<view.byteLength;i++) view[i]=parseInt(bytes.slice(2*i,2*i+2),16);
      return view;
    },
    randomUUID() {
      const bytes = hcall("crypto.random",16);
      const variant = ((parseInt(bytes[16],16)&3)|8).toString(16);
      return bytes.slice(0,8)+"-"+bytes.slice(8,12)+"-4"+bytes.slice(13,16)+"-"+variant+bytes.slice(17,20)+"-"+bytes.slice(20);
    }
  });
  // Timer ids come from Go, which fires them with fireTimer(id, keep).
  const timers = new Map();
  function addTimer(fn, ms, repeat, a) {
    const id = Number(hraw(K_SET_TIMER, repeat ? "1" : "0", undefined, Number(ms) || 0));
    timers.set(id, () => fn(...a));
    return id;
  }
  g.setTimeout = function (fn, ms, ...a) {
    if (typeof fn !== "function") throw new TypeError("setTimeout: callback must be a function");
    return addTimer(fn, ms, false, a);
  };
  g.setInterval = function (fn, ms, ...a) {
    if (typeof fn !== "function") throw new TypeError("setInterval: callback must be a function");
    return addTimer(fn, ms, true, a);
  };
  g.clearTimeout = g.clearInterval = function (id) {
    if (id === undefined || id === null) return;
    const n = Number(id);
    timers.delete(n);
    hraw(K_CLEAR_TIMER, undefined, undefined, n);
  };
  function fireTimer(id, keep) {
    const n = Number(id);
    const f = timers.get(n);
    if (!f) return;
    if (keep !== "1") timers.delete(n);
    f();
  }
  function dropTimer(id) { timers.delete(Number(id)); }
  g.queueMicrotask = function (fn) {
    if (typeof fn !== "function") throw new TypeError("queueMicrotask: callback must be a function");
    Promise.resolve().then(() => fn());
  };
  const consoleFn = (level) => (...a) => hlog(level, a.map(safeString).join(" "), "");
  g.console = freeze({
    log: consoleFn("info"), info: consoleFn("info"), warn: consoleFn("warn"),
    error: consoleFn("error"), debug: consoleFn("debug"),
  });
  g.require = function (name) {
    if (name === "@cicord/sdk") return sdk;
    throw new Error(`cannot require "${name}": only @cicord/sdk is available at run time (bundle other dependencies)`);
  };

  return { errInfo, describe, loadPlugin, setup, dispatch, settle, fireTimer, dropTimer, takeRejections };
})
