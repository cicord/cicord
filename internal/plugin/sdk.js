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

  function errInfo(e) {
    try {
      if (e instanceof Error) {
        return JSON.stringify({
          name: String(e.name || "Error"),
          message: String(e.message),
          stack: typeof e.stack === "string" ? e.stack : "",
          capability: e instanceof CapabilityError ? e.capability : undefined,
        });
      }
      return JSON.stringify({ name: "Error", message: "non-Error value thrown: " + safeString(e) });
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
    for (const r of rejections.values()) out.push(errInfo(r));
    rejections.clear();
    return JSON.stringify(out);
  }

  function msgIn(m) {
    if (typeof m === "string") return { content: m };
    if (m === null || typeof m !== "object") throw new TypeError("message must be a string or a message payload");
    return m;
  }

  // ─────────────────────────────────────────────────────────── entities

  function mkUser(d) {
    if (!d) return undefined;
    return freeze({
      id: d.id,
      username: d.username || "",
      displayName: d.displayName || d.username || "",
      avatarUrl: d.avatarUrl || undefined,
      bot: !!d.bot,
      mention: "<@" + d.id + ">",
    });
  }

  function mkRole(d) {
    if (!d) return undefined;
    return freeze({ id: d.id, key: d.key || undefined, name: d.name || "", color: d.color || undefined, mention: d.mention });
  }

  function mkAttachment(d) {
    return freeze({ id: d.id, filename: d.filename, url: d.url, contentType: d.contentType || undefined, size: d.size });
  }

  const TextChannelProto = {
    async send(message) {
      return mkMessage(await acall("channel.send", { channelId: this.id, message: msgIn(message) }));
    },
    async messages(options) {
      const o = options || {};
      const list = await acall("channel.messages", { channelId: this.id, limit: o.limit, before: o.before, after: o.after });
      return list.map(mkMessage);
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
    c.mention = "<#" + d.id + ">";
    return freeze(c);
  }

  const MessageProto = {
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
    m.content = d.content || "";
    m.author = mkUser(d.author);
    m.member = d.member ? mkMember(d.member) : undefined;
    m.channel = mkChannel(d.channel);
    m.attachments = freeze((d.attachments || []).map(mkAttachment));
    m.createdAt = d.createdAt ? new Date(d.createdAt) : new Date(NaN);
    return freeze(m);
  }

  const memberRoles = new WeakMap();
  const rolesOf = (m) => memberRoles.get(m) || [];

  const MemberProto = {
    get roles() { return freeze(rolesOf(this).slice()); },
    hasRole(role) {
      const id = hcall("resolve.role", { ref: role });
      return rolesOf(this).some((r) => r.id === id);
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
    async send(message) {
      return mkMessage(await acall("member.send", { userId: this.id, message: msgIn(message) }));
    },
    toString() { return this.mention; },
  };

  function mkMember(d) {
    if (!d) return undefined;
    const m = Object.create(MemberProto);
    m.user = mkUser(d.user);
    m.id = d.user.id;
    m.nickname = d.nick || undefined;
    m.displayName = d.displayName || m.user.displayName;
    m.mention = "<@" + d.user.id + ">";
    m.joinedAt = d.joinedAt ? new Date(d.joinedAt) : new Date(NaN);
    memberRoles.set(m, (d.roles || []).map(mkRole));
    return freeze(m);
  }

  // ─────────────────────────────────────────────────────────── interactions

  const handles = new WeakMap();
  const hOf = (it) => handles.get(it);

  function replyIn(m) {
    if (typeof m === "string") return { content: m };
    if (m === null || typeof m !== "object") throw new TypeError("reply must be a string or a message payload");
    return m;
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
    o.options = mkOptions(d.options);
    return freeze(o);
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
  const hlog = (level, message, fields) => hraw(K_LOG, message, fields, LEVELS[level]);

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
      id: d.server.id,
      key: d.server.key,
      name: d.server.name,
      iconUrl: d.server.iconUrl || undefined,
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
        await acall("server.deleteChannel", { ref: ref && typeof ref === "object" ? { id: ref.id } : ref, reason });
      },
    });

    const kv = freeze({
      async get(key) { return acall("kv.get", { key }); },
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
      async link(path, options) {
        const o = options || {};
        return acall("portal.link", { path, userId: o.member ? o.member.id : undefined, expiresInSeconds: o.expiresInSeconds });
      },
    });

    return freeze({
      plugin: freeze(d.plugin),
      bot: freeze({ key: d.bot.key, user: mkUser(d.bot.user) }),
      server,
      config: deepFreeze(d.config || {}),
      log: freeze({ debug: logFn("debug"), info: logFn("info"), warn: logFn("warn"), error: logFn("error") }),
      kv,
      schedule,
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

  function defineRoutes(routes) { return routes; }

  const sdk = freeze({ definePlugin, command, defineRoutes, CapabilityError });

  const entries = (o) => (o && typeof o === "object" ? Object.keys(o).map((k) => [k, o[k]]) : []);
  const fns = (o) => entries(o).map(([name, f]) => ({ name, type: typeof f }));

  function describe() {
    const def = plugin;
    const sections = {};
    for (const k of ["events", "commands", "components", "modals", "schedules", "tasks", "subscriptions", "routes"]) {
      sections[k] = typeof def[k];
    }
    return JSON.stringify({
      keys: Object.keys(def),
      sections,
      onStart: typeof def.onStart,
      onStop: typeof def.onStop,
      events: fns(def.events),
      commands: entries(def.commands).map(([name, c]) => ({
        name,
        description: c && c.description,
        ephemeral: c && c.ephemeral,
        permissions: c && c.permissions,
        run: typeof (c && c.run),
        optionsType: typeof (c && c.options),
        options: entries(c && c.options).map(([oname, o]) => Object.assign({}, o, { name: oname })),
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

  function findHandler(kind, name) {
    switch (kind) {
      case "start": return plugin.onStart ? [plugin, plugin.onStart] : null;
      case "stop": return plugin.onStop ? [plugin, plugin.onStop] : null;
      case "event": { const f = own(plugin.events, name); return f ? [plugin.events, f] : null; }
      case "command": { const c = own(plugin.commands, name); return c && c.run ? [c, c.run] : null; }
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
      case "memberJoin": return [mkMember(d.member)];
      case "memberLeave": return [mkUser(d.user)];
      case "memberUpdate": return [mkMember(d.member), d.previous ? mkMember(d.previous) : undefined];
      case "messageCreate":
      case "messageUpdate": return [mkMessage(d.message)];
      case "messageDelete": return [freeze({ id: d.id, channel: mkChannel(d.channel) })];
      case "reactionAdd":
      case "reactionRemove": return [freeze({ message: mkMessage(d.message), user: mkUser(d.user), emoji: d.emoji })];
    }
    return [];
  }

  function args(kind, name, d) {
    switch (kind) {
      case "event": return eventArgs(name, d);
      case "command": return [mkCommandInteraction(d)];
      case "component": return [mkComponentInteraction(d)];
      case "modal": return [mkModalInteraction(d)];
      case "task":
      case "subscription": return [d.payload];
      case "route": return [freeze({
        params: freeze(d.params || {}),
        query: freeze(d.query || {}),
        body: d.body,
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
          if (kind !== "route") return ok();
          let s;
          try { s = JSON.stringify(routeOut(v)); } catch (e) { return fail(e); }
          ok(s);
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
