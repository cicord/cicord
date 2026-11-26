import { Button, Notice } from "@cicord/ui";
import { useEffect, useRef, useState } from "react";
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
    const finish = (widget?: Widget) => {
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
  | { id: string; method: "visual"; image: string; choices: number[]; prompt: string }
  | { id: string; method: Provider; siteKey: string };
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
  const visual = value.method === "visual";
  return (
    <form
      className="feature-form"
      onSubmit={(e) => {
        e.preventDefault();
        void write.run<Exclude<Challenge, { id: string }>>(
          "POST",
          "/challenge",
          { id: value.id, ...(visual ? { answer } : token ? { token } : {}) },
          (r) => {
            setToken("");
            setResult(r);
          },
        );
      }}
    >
      {visual ? (
        <fieldset>
          <legend>{value.prompt}</legend>
          {value.image.startsWith("data:image/png;base64,") ? (
            <img
              className="feature-challenge"
              src={value.image}
              alt={c("Verification image", "Image de vérification")}
            />
          ) : null}
          <div className="feature-actions">
            {value.choices.map((n) => (
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
          write.pending || (visual ? answer === undefined : value.method !== "button" && !token)
        }
      >
        {c("Verify my account", "Vérifier mon compte")}
      </Button>
      {write.error ? <Failure error={write.error} retry={reload} /> : null}
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
  const read = useRead<{ result: string; method: string | null; at: number | null }>(
    "/return/" + encodeURIComponent(id),
  );
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
