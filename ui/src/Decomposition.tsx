import type { Summary } from "./api/types";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildDecompositionOption } from "./charts/options";
import { formatMs } from "./format";

// TTFT split into the measured edge RTT and everything beyond it.
//
// This is the number nobody else publishes, and the reason the whole cycle is
// aligned — both halves come from the SAME five-minute tick, so the subtraction
// is exact rather than interpolated.
//
// It sits below "The wire itself" rather than at the top of the page: the split
// is a subtraction of the handshake, and a reader who has not seen the
// handshake measured yet has no reason to accept the minuend.
export function Decomposition({
  summary,
  edgeMs,
}: {
  summary: Summary | null;
  edgeMs: number | null;
}) {
  const models = (summary?.models ?? []).map((m) => ({
    id: m.model_id,
    ttft: m.ttft.sufficient ? m.ttft.p50_ms : null,
    edge: edgeMs,
  }));

  const hasData = models.some((m) => m.ttft !== null && m.edge !== null);

  return (
    <Card
      title="Where the time goes"
      subtitle="Time to first token, split into the measured TCP handshake to MiMo's edge and the remainder. Both halves come from the same 5-minute cycle."
    >
      {hasData ? (
        <>
          <EChart
            option={buildDecompositionOption(models)}
            height={120}
            ariaLabel="Time to first token split into edge round-trip and server-side time, per model"
          />
          <ul className="mt-3 space-y-1 text-label text-muted">
            {models.map((m) =>
              m.ttft !== null && m.edge !== null ? (
                <li key={m.id} className="num">
                  {m.id}: {formatMs(m.ttft)} = {formatMs(m.edge)} to the edge +{" "}
                  {formatMs(Math.max(0, m.ttft - m.edge))} beyond it
                </li>
              ) : null,
            )}
          </ul>
        </>
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
      <p className="mt-4 border-l-2 border-accent/60 bg-accent/5 px-4 py-3 text-label text-muted">
        <strong className="text-ink">
          Called “server-side time”, never “model time.”
        </strong>{" "}
        The handshake terminates at the TLS edge. Xiaomi runs no European GPUs,
        so whatever backhaul exists between that edge and the actual compute
        sits <em>inside</em> this remainder, along with queueing, prefill and
        scheduling. Calling it model time would be a claim the measurement
        cannot support.
      </p>
    </Card>
  );
}
