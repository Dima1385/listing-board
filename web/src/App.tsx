import { ChangeEvent, FormEvent, useEffect, useState } from "react";

type Step = { label: string; state: string };

type Job = {
  id: string;
  title: string;
  address: string;
  status: string;
  error: string;
  filename: string;
  checklist: Step[];
};

type Account = { id: string; email: string };

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "include", ...init });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    const message = data && typeof data.error === "string" ? data.error : res.statusText;
    throw new Error(message);
  }
  return data as T;
}

function uploadBrochure(jobId: string, file: File): Promise<Job> {
  const body = new FormData();
  body.append("file", file);
  return api<Job>("/api/jobs/" + jobId + "/file", { method: "POST", body });
}

function message(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

const POLL_MS = 1000;

export function App() {
  const [me, setMe] = useState<Account | null>(null);
  const [ready, setReady] = useState(false);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selected, setSelected] = useState<Job | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api<Account>("/api/me")
      .then(setMe)
      .catch(() => setMe(null))
      .finally(() => setReady(true));
  }, []);

  useEffect(() => {
    if (!me) {
      setJobs([]);
      return;
    }
    api<Job[]>("/api/jobs")
      .then(setJobs)
      .catch((err: Error) => setError(err.message));
  }, [me]);

  if (!ready) {
    return <p className="loading">Loading…</p>;
  }
  if (!me) {
    return <Auth onIn={setMe} />;
  }

  return (
    <main className="board">
      <header className="topbar">
        <h1>Listing board</h1>
        <div className="account">
          <span>{me.email}</span>
          <button
            className="ghost"
            onClick={() => {
              api("/api/logout", { method: "POST" }).finally(() => {
                setMe(null);
                setSelected(null);
                setError("");
              });
            }}
          >
            Log out
          </button>
        </div>
      </header>
      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      <Create
        onCreated={(job) => {
          setJobs((current) => [job, ...current]);
          setSelected(job);
          setError("");
        }}
        onError={setError}
      />
      <section className="layout">
        <ul className="list" aria-label="Sheets">
          {jobs.length === 0 ? <li className="empty">No sheets yet. Create one above.</li> : null}
          {jobs.map((job) => (
            <li key={job.id}>
              <button
                className={selected?.id === job.id ? "row on" : "row"}
                aria-current={selected?.id === job.id ? "true" : undefined}
                onClick={() => setSelected(job)}
              >
                <span className="row-text">
                  <strong>{job.title}</strong>
                  <span>{job.address}</span>
                </span>
                <span className={"status " + job.status}>{job.status}</span>
              </button>
            </li>
          ))}
        </ul>
        {selected ? (
          <Sheet
            job={selected}
            onChange={(job) => {
              setSelected(job);
              setJobs((current) => current.map((item) => (item.id === job.id ? job : item)));
            }}
            onError={setError}
          />
        ) : (
          <p className="placeholder">Select a sheet to see its checklist.</p>
        )}
      </section>
    </main>
  );
}

function Auth({ onIn }: { onIn: (account: Account) => void }) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError("");
    try {
      const account = await api<Account>("/api/" + mode, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      onIn(account);
    } catch (err) {
      setError(message(err, "Could not sign in"));
    }
  }

  return (
    <main className="auth">
      <h1>Listing board</h1>
      <p className="lead">
        {mode === "login" ? "Sign in to manage your listing sheets." : "Create an account to manage your listing sheets."}
      </p>
      <form onSubmit={submit}>
        <label>
          Email
          <input value={email} onChange={(event) => setEmail(event.target.value)} type="email" required />
        </label>
        <label>
          Password
          <input value={password} onChange={(event) => setPassword(event.target.value)} type="password" required />
        </label>
        {error ? (
          <p className="error" role="alert">
            {error}
          </p>
        ) : null}
        <button type="submit">{mode === "login" ? "Sign in" : "Create account"}</button>
      </form>
      <button className="ghost" onClick={() => setMode(mode === "login" ? "register" : "login")}>
        {mode === "login" ? "Need an account?" : "Already have an account?"}
      </button>
    </main>
  );
}

function Create({ onCreated, onError }: { onCreated: (job: Job) => void; onError: (message: string) => void }) {
  const [title, setTitle] = useState("");
  const [address, setAddress] = useState("");
  const [file, setFile] = useState<File | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file) {
      onError("Choose a brochure file");
      return;
    }
    const form = event.currentTarget;
    let job: Job;
    try {
      job = await api<Job>("/api/jobs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title, address }),
      });
    } catch (err) {
      onError(message(err, "Could not create the sheet"));
      return;
    }
    form.reset();
    setTitle("");
    setAddress("");
    setFile(null);
    try {
      onCreated(await uploadBrochure(job.id, file));
    } catch (err) {
      onCreated(job);
      onError(message(err, "Could not attach the brochure"));
    }
  }

  return (
    <form className="create" onSubmit={submit}>
      <label>
        Title
        <input value={title} onChange={(event) => setTitle(event.target.value)} required />
      </label>
      <label>
        Address
        <input value={address} onChange={(event) => setAddress(event.target.value)} required />
      </label>
      <label>
        Brochure
        <input
          type="file"
          accept=".pdf,.txt,application/pdf,text/plain"
          onChange={(event) => setFile(event.target.files?.[0] ?? null)}
          required
        />
      </label>
      <button type="submit">Create sheet</button>
    </form>
  );
}

function Sheet({
  job,
  onChange,
  onError,
}: {
  job: Job;
  onChange: (job: Job) => void;
  onError: (message: string) => void;
}) {
  useEffect(() => {
    if (job.status !== "running") {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(() => {
      api<Job>("/api/jobs/" + job.id)
        .then((next) => {
          if (!cancelled) {
            onChange(next);
          }
        })
        .catch(() => {});
    }, POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [job.id, job.status]);

  async function start() {
    onError("");
    try {
      onChange(await api<Job>("/api/jobs/" + job.id + "/start", { method: "POST" }));
    } catch (err) {
      onError(message(err, "Could not start the sheet"));
    }
  }

  async function attach(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) {
      return;
    }
    onError("");
    try {
      onChange(await uploadBrochure(job.id, file));
    } catch (err) {
      event.target.value = "";
      onError(message(err, "Could not attach the brochure"));
    }
  }

  const current = job.status === "running" ? job.checklist.findIndex((step) => step.state !== "done") : -1;

  return (
    <article className="sheet">
      <div className="sheet-head">
        <div>
          <h2>{job.title}</h2>
          <p className="address">{job.address}</p>
        </div>
        <span className={"status " + job.status}>{job.status}</span>
      </div>
      {job.filename ? (
        <p className="file">
          Brochure{" "}
          <a href={"/api/jobs/" + job.id + "/file"} download>
            {job.filename}
          </a>
        </p>
      ) : null}
      {job.error ? (
        <p className="error" role="alert">
          {job.error}
        </p>
      ) : null}
      <ol className="steps" aria-live="polite">
        {job.checklist.map((step, index) => (
          <li key={step.label} className={index === current ? step.state + " current" : step.state}>
            {step.label}
          </li>
        ))}
      </ol>
      {job.status === "draft" && !job.filename ? (
        <label>
          Brochure
          <input type="file" accept=".pdf,.txt,application/pdf,text/plain" onChange={attach} />
        </label>
      ) : null}
      {job.status === "draft" && job.filename ? <button onClick={start}>Start</button> : null}
    </article>
  );
}
