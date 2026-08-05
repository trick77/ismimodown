import type { Summary } from "./api/types";
import { Card } from "./ui";
import { EChart } from "./charts/EChart";
import { buildDecompositionOption } from "./charts/options";

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
        <EChart
          option={buildDecompositionOption(models)}
          height={120}
          ariaLabel="Time to first token split into edge round-trip and server-side time, per model"
        />
      ) : (
        <p className="font-serif italic text-faint">
          Not enough data yet — first samples within 5 minutes.
        </p>
      )}
      <p className="mt-4 border-l-2 border-accent/60 bg-accent/5 px-4 py-3 text-label text-muted">
        <strong className="text-ink">
          Called “server-side time”, never “model time.”
        </strong>{" "}
        The handshake measured here terminates at the TLS edge. Everything after
        that — any backhaul between that edge and wherever the request is
        computed, plus queueing, prefill and scheduling — sits <em>inside</em>{" "}
        this remainder, and this measurement cannot separate them. Calling it
        model time would claim a split it cannot show.
      </p>
    </Card>
  );
}
