import { Component, type ReactNode } from "react";
import { Masthead } from "./Masthead";

// Catches a render error anywhere below it and shows a way out instead of a
// blank page.
//
// The case this exists for is a deploy, not a bug in the usual sense. A tab
// opened before it keeps running the old bundle and keeps refetching, so the
// first response whose shape the old code no longer matches — a renamed or
// dropped key it maps over — throws mid-render. Without a boundary React
// unmounts the whole root and the reader is left on an empty dark page that
// says nothing, on the site whose one job is to say whether something is down.
//
// Reloading is the fix in that case, because it fetches the bundle that
// matches the server, so that is the one thing the fallback offers.
//
// The fallback renders nothing that reads data: the masthead is static, and it
// keeps the page's heading in place, so a reader (or a crawler that rendered
// mid-deploy) still lands on "Is Xiaomi MiMo down?" rather than a bare error.
type Props = {
  children: ReactNode;
  // Injected by the tests: jsdom's location.reload cannot be stubbed.
  onReload?: () => void;
};

// The message is shown, small, so a reader's report or screenshot carries
// something to tell a stale bundle from a real bug.
type State = { error: string | null };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(err: unknown): State {
    return { error: (err as Error)?.message || String(err) };
  }

  render() {
    if (this.state.error === null) return this.props.children;
    const reload = this.props.onReload ?? (() => window.location.reload());
    return (
      <>
        <div className="aura" aria-hidden="true" />
        <div className="relative z-10 mx-auto max-w-[1180px] px-4 pb-24 sm:px-8">
          <Masthead />
          <main>
            <section role="alert" className="card p-4 sm:p-5">
              <p className="font-serif text-body text-ink-dim">
                This page stopped drawing after an error.
              </p>
              <p className="mt-2 text-label text-muted">
                Usually the site was updated while this tab was open. Reloading
                fetches the current version.
              </p>
              <button type="button" className="pill mt-4" onClick={reload}>
                Reload
              </button>
              <p className="num mt-4 break-words text-micro text-faint">
                {this.state.error}
              </p>
            </section>
          </main>
        </div>
      </>
    );
  }
}
