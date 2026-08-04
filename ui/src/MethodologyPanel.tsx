import { useEffect, useState } from "react";
import { getMethodology } from "./api";
import { Card } from "./ui";

// The methodology is fetched from the server rather than duplicated here, so
// the page cannot drift from what the daemon is actually doing. It is readable
// from minute one, before any sample exists — which is the point: the method is
// what makes the numbers worth reading.
export function MethodologyPanel() {
  const [doc, setDoc] = useState<Record<string, unknown> | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    getMethodology(controller.signal)
      .then(setDoc)
      .catch(() => {
        // Non-fatal: the rest of the page still works.
      });
    return () => controller.abort();
  }, []);

  return (
    <Card title="Methodology">
      {doc ? (
        <dl className="grid gap-4">
          {Object.entries(doc).map(([key, value]) => (
            <div key={key}>
              <dt className="num text-micro uppercase tracking-wider text-faint">
                {key.replace(/_/g, " ")}
              </dt>
              <dd className="mt-1 text-label text-muted">
                {typeof value === "string" ? (
                  value
                ) : (
                  <pre className="num overflow-x-auto whitespace-pre-wrap text-micro text-muted">
                    {JSON.stringify(value, null, 2)}
                  </pre>
                )}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="font-serif italic text-faint">Loading methodology…</p>
      )}
    </Card>
  );
}
