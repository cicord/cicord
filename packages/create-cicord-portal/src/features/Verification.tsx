import { CicordApiError } from "@cicord/client";
import { Button, Notice } from "@cicord/ui";
import { useEffect, useId, useRef, useState } from "react";
import {
  Empty,
  Failure,
  Outcome,
  Paging,
  Result,
  useCopy,
  useCursor,
  useDate,
  useRead,
  useScope,
  useWrite,
} from "./shared.tsx";

type Provider = "turnstile" | "hcaptcha";
interface Widget {
  render(
    node: HTMLElement,
    opts: {
      sitekey: string;
      callback: (token: string) => void;
      "error-callback": () => void;
      "expired-callback": () => void;
    },
  ): string;
  remove(id: string): void;
}
const vendors = {
  turnstile: "https://challenges.cloudflare.com/turnstile/v0/api.js",
  hcaptcha: "https://js.hcaptcha.com/1/api.js",
};
const vendorsLoading = new Map<Provider, Promise<Widget>>();
function loadWidget(provider: Provider): Promise<Widget> {
  const win = window as typeof window & Partial<Record<Provider, Widget>> & Record<string, unknown>;
  if (win[provider]) return Promise.resolve(win[provider]);
  const existing = vendorsLoading.get(provider);
  if (existing) return existing;
  const waiting = new Promise<Widget>((resolve, reject) => {
    const ready = "cicordReady_" + provider,
      script = document.createElement("script");
    let settled = false;
    const finish = (widget?: Widget) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      delete win[ready];
      if (widget) resolve(widget);
      else {
        script.remove();
        vendorsLoading.delete(provider);
        reject(new Error("widget unavailable"));
      }
    };
    const timer = window.setTimeout(() => finish(), 20000);
    win[ready] = () => finish(win[provider]);
    script.async = true;
    script.src = vendors[provider] + "?render=explicit&onload=" + ready;
    script.referrerPolicy = "no-referrer";
    script.addEventListener("error", () => finish(), { once: true });
    document.head.append(script);
  });
  vendorsLoading.set(provider, waiting);
  return waiting;
}
function Captcha({
  provider,
  siteKey,
  onToken,
}: {
  provider: Provider;
  siteKey: string;
  onToken: (token: string) => void;
}) {
  const target = useRef<HTMLDivElement>(null),
    [failed, setFailed] = useState(false),
    [attempt, retry] = useState(0);
  const c = useCopy();
  useEffect(() => {
    let alive = true,
      widget: Widget | undefined,
      id: string | undefined;
    const node = target.current;
    const error = () => {
      if (alive) {
        onToken("");
        setFailed(true);
      }
    };
    const init = async () => {
      const w = await loadWidget(provider);
      if (!alive || !node) return;
      widget = w;
      id = w.render(node, {
        sitekey: siteKey,
        callback: (t) => {
          if (alive) {
            onToken(t);
            setFailed(false);
          }
        },
        "expired-callback": () => {
          if (alive) onToken("");
        },
        "error-callback": error,
      });
    };
    void init().catch(error);
    return () => {
      alive = false;
      if (widget && id !== undefined) widget.remove(id);
    };
  }, [provider, siteKey, onToken, attempt]);
  return (
    <>
      <div ref={target} />
      {failed ? (
        <Notice
          tone="error"
          title={c(
            "The verification widget could not load.",
            "Le contrôle de vérification n’a pas pu se charger.",
          )}
          action={
            <Button
              onClick={() => {
                setFailed(false);
                retry((n) => n + 1);
              }}
            >
              {c("Reload", "Recharger")}
            </Button>
          }
        />
      ) : null}
    </>
  );
}
type Challenge =
  | { verified: boolean }
  | { state: string; retryAt?: number }
  | { id: string; method: "button" }
  | {
      id: string;
      method: "visual";
      kind?: undefined;
      image: string;
      choices: number[];
      prompt: string;
    }
  | TypedChallenge
  | { id: string; method: Provider; siteKey: string };
/**
 * A code drawn in the image, typed by the member: `length` characters (4 to 8) from
 * `charset` ("alphanumeric" by default: A–Z and 0–9). The answer is posted as a string,
 * trimmed and upper-cased; the plugin compares it case-insensitively.
 */
interface TypedChallenge {
  id: string;
  /** The plugin's method name ("typed"; "visual" is accepted too): `kind` decides the form. */
  method: "typed" | "visual";
  kind: "typed";
  image: string;
  length: number;
  charset?: "digits" | "alphanumeric";
  prompt: string;
}
function TypedCode({
  value,
  code,
  setCode,
  rejected,
}: {
  value: TypedChallenge;
  code: string;
  setCode: (code: string) => void;
  rejected: boolean;
}) {
  const c = useCopy(),
    id = useId();
  const digits = value.charset === "digits";
  const length = Math.min(8, Math.max(4, Math.trunc(value.length) || 4));
  return (
    <fieldset>
      <legend>{value.prompt}</legend>
      {value.image.startsWith("data:image/png;base64,") ? (
        <img
          className="feature-challenge"
          src={value.image}
          alt={c(
            "Verification image with a code to type",
            "Image de vérification contenant un code à saisir",
          )}
        />
      ) : null}
      <label htmlFor={id + "-code"}>
        {c("Code shown in the image", "Code affiché dans l’image")}
      </label>
      <input
        id={id + "-code"}
        name="answer"
        className="feature-code"
        required
        value={code}
        minLength={length}
        maxLength={length + 4}
        pattern={
          String.raw`\s*` + (digits ? "[0-9]" : "[A-Za-z0-9]") + `{${length}}` + String.raw`\s*`
        }
        inputMode={digits ? "numeric" : "text"}
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize={digits ? "off" : "characters"}
        spellCheck={false}
        readOnly={rejected}
        aria-invalid={rejected || undefined}
        aria-describedby={id + (rejected ? "-error" : "-hint")}
        onChange={(e) => setCode(e.target.value)}
      />
      {rejected ? (
        <p id={id + "-error"} className="feature-field-error" role="alert">
          {c(
            "This code was not accepted, and the challenge can only be tried once. Get a new one.",
            "Ce code n’a pas été accepté, et la vérification ne peut être tentée qu’une fois. Demandez-en une nouvelle.",
          )}
        </p>
      ) : (
        <p id={id + "-hint"} className="feature-hint">
          {digits
            ? c(`${length} digits.`, `${length} chiffres.`)
            : c(
                `${length} letters or digits. Upper and lower case are the same.`,
                `${length} lettres ou chiffres. Majuscules et minuscules sont équivalentes.`,
              )}
        </p>
      )}
    </fieldset>
  );
}
const typedAnswer = (value: TypedChallenge, code: string) => {
  const answer = code.trim().toUpperCase();
  const length = Math.min(8, Math.max(4, Math.trunc(value.length) || 4));
  const valid = new RegExp(
    (value.charset === "digits" ? "^[0-9]" : "^[A-Z0-9]") + `{${length}}$`,
  ).test(answer);
  return valid ? answer : undefined;
};
function ChallengeState({ state }: { state: Exclude<Challenge, { id: string }> }) {
  const c = useCopy(),
    date = useDate();
  if ("verified" in state)
    return (
      <Notice
        title={
          state.verified
            ? c("Your account is verified.", "Votre compte est vérifié.")
            : c("Verification failed.", "La vérification a échoué.")
        }
      />
    );
  if (state.state === "hold")
    return (
      <Notice
        title={c("A moderator will review your account.", "Un modérateur examinera votre compte.")}
      />
    );
  if (state.state === "delay")
    return (
      <Notice title={c("Verification is on hold.", "La vérification est en attente.")}>
        {state.retryAt ? date(state.retryAt) : null}
      </Notice>
    );
  return <Notice title={c("Verification was declined.", "La vérification a été refusée.")} />;
}
function Attempt({
  value,
  reload,
}: {
  value: Extract<Challenge, { id: string }>;
  reload: () => void;
}) {
  const c = useCopy(),
    write = useWrite();
  const [answer, setAnswer] = useState<number>(),
    [code, setCode] = useState(""),
    [token, setToken] = useState(""),
    [result, setResult] = useState<Exclude<Challenge, { id: string }>>();
  if (result)
    return (
      <>
        <ChallengeState state={result} />
        {"verified" in result && !result.verified ? (
          <Button onClick={reload}>{c("Try a new challenge", "Nouvelle vérification")}</Button>
        ) : null}
      </>
    );
  const typed = "kind" in value && value.kind === "typed" ? value : undefined;
  const pick = value.method === "visual" && value.kind !== "typed" ? value : undefined;
  const visual = pick !== undefined;
  const typedCode = typed ? typedAnswer(typed, code) : undefined;
  // Every checked attempt consumes the challenge: after a 422 only a new one can be tried.
  // Other errors (429 lock, 503, network) keep the generic failure notice.
  const rejected =
    typed !== undefined && write.error instanceof CicordApiError && write.error.status === 422;
  const failed = typed !== undefined && write.error !== undefined;
  return (
    <form
      className="feature-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (typed && (typedCode === undefined || failed)) return;
        void write.run<Exclude<Challenge, { id: string }>>(
          "POST",
          "/challenge",
          {
            id: value.id,
            ...(typed ? { answer: typedCode } : visual ? { answer } : token ? { token } : {}),
          },
          (r) => {
            setToken("");
            setResult(r);
          },
        );
      }}
    >
      {typed ? <TypedCode value={typed} code={code} setCode={setCode} rejected={rejected} /> : null}
      {pick ? (
        <fieldset>
          <legend>{pick.prompt}</legend>
          {pick.image.startsWith("data:image/png;base64,") ? (
            <img
              className="feature-challenge"
              src={pick.image}
              alt={c("Verification image", "Image de vérification")}
            />
          ) : null}
          <div className="feature-actions">
            {pick.choices.map((n) => (
              <label className="feature-choice" key={n}>
                <input
                  type="radio"
                  name="answer"
                  value={n}
                  checked={answer === n}
                  onChange={() => setAnswer(n)}
                  required
                />
                {n}
              </label>
            ))}
          </div>
        </fieldset>
      ) : null}
      {value.method === "turnstile" || value.method === "hcaptcha" ? (
        <Captcha provider={value.method} siteKey={value.siteKey} onToken={setToken} />
      ) : null}
      <Button
        type="submit"
        disabled={
          write.pending ||
          failed ||
          (typed
            ? typedCode === undefined
            : visual
              ? answer === undefined
              : value.method !== "button" && !token)
        }
      >
        {c("Verify my account", "Vérifier mon compte")}
      </Button>
      {rejected ? (
        <Button variant="secondary" onClick={reload}>
          {c("Try a new challenge", "Nouvelle vérification")}
        </Button>
      ) : write.error ? (
        <Failure error={write.error} retry={reload} />
      ) : null}
    </form>
  );
}
export function Verification() {
  const read = useRead<Challenge>("/challenge");
  return (
    <Result read={read}>
      {(v) =>
        "id" in v ? (
          <Attempt key={v.id} value={v} reload={read.reload} />
        ) : (
          <ChallengeState state={v} />
        )
      }
    </Result>
  );
}
export function Recovery({ id }: { id: string }) {
  const c = useCopy(),
    date = useDate();
  const read = useRead<{
    result: string;
    method: string | null;
    at: number | string | null;
    retryAt?: number;
    invite?: string;
  }>("/return/" + encodeURIComponent(id));
  return (
    <Result read={read}>
      {(r) => (
        <dl className="feature-facts">
          <dt>{c("Result", "Résultat")}</dt>
          <dd>{r.result}</dd>
          {r.method ? (
            <>
              <dt>{c("Method", "Méthode")}</dt>
              <dd>{r.method}</dd>
            </>
          ) : null}
          {r.retryAt ? (
            <>
              <dt>{c("Eligible to return", "Retour possible le")}</dt>
              <dd>{date(r.retryAt)}</dd>
            </>
          ) : null}
          {r.invite && /^https:\/\/discord\.gg\/[A-Za-z0-9_-]+$/.test(r.invite) ? (
            <>
              <dt>{c("Invitation", "Invitation")}</dt>
              <dd>
                <a href={r.invite} referrerPolicy="no-referrer">
                  {c("Return to Discord", "Rejoindre le serveur Discord")}
                </a>
              </dd>
            </>
          ) : null}
          {r.at ? (
            <>
              <dt>{c("Date", "Date")}</dt>
              <dd>{date(r.at)}</dd>
            </>
          ) : null}
        </dl>
      )}
    </Result>
  );
}
export function Queue() {
  const c = useCopy(),
    date = useDate(),
    page = useCursor(),
    write = useWrite(),
    { plugin } = useScope();
  const read = useRead<{ items: { userId: string; joined: number }[]; cursor?: string }>("/queue", {
    cursor: page.value,
  });
  return (
    <>
      <Result read={read}>
        {(p) => (
          <>
            {p.items.length ? (
              <ul className="feature-list">
                {p.items.map((m) => (
                  <li key={m.userId}>
                    <h2>{m.userId}</h2>
                    <p>
                      {c("Joined", "Arrivée")} · {date(m.joined)}
                    </p>
                    {plugin.routes.includes("POST /queue/:id") ? (
                      <div className="feature-actions">
                        {(["approve", "deny"] as const).map((d) => (
                          <Button
                            key={d}
                            variant={d === "approve" ? "primary" : "secondary"}
                            disabled={write.pending}
                            onClick={() =>
                              void write.run(
                                "POST",
                                "/queue/" + encodeURIComponent(m.userId),
                                { decision: d },
                                read.reload,
                              )
                            }
                          >
                            {d === "approve" ? c("Approve", "Approuver") : c("Decline", "Refuser")}
                          </Button>
                        ))}
                      </div>
                    ) : null}
                  </li>
                ))}
              </ul>
            ) : (
              <Empty />
            )}
            <Paging previous={page.back} next={p.cursor ? () => page.next(p.cursor!) : undefined} />
          </>
        )}
      </Result>
      <Outcome write={write} />
    </>
  );
}
