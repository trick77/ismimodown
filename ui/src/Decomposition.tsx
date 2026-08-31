import type { Summary } from "./api/types";
import { Card, NoChart } from "./ui";
import { PANEL_COPY } from "./copy";
import { EChart } from "./charts/EChart";
import { buildDecompositionOption } from "./charts/options";

// Shorter than the shared default: this is two stacked bars, not a time series,
// and at 240 it was mostly empty plot. Named because the placeholder below has
// to reserve the same height, and a plot and its stand-in disagreeing about how
// tall they are is exactly the layout shift both exist to avoid.
const HEIGHT = 120;

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
    <Card {...PANEL_COPY.decomposition}>
      {hasData ? (
        <EChart
          option={buildDecompositionOption(models)}
          height={HEIGHT}
          ariaLabel="Time to first token split into edge round-trip and server-side time, per model"
        />
      ) : (
        <NoChart height={HEIGHT}>
          Not enough data yet — first samples within a few minutes.
        </NoChart>
      )}
      <p className="mt-4 border-l-2 border-accent/60 bg-accent/5 px-4 py-3 text-label text-muted">
        The handshake measured here terminates at the TLS edge. Everything after
        that — any backhaul between that edge and wherever the request is
        computed, plus queueing, prefill and scheduling — sits <em>inside</em>{" "}
        this remainder, and this measurement cannot separate them.
      </p>
    </Card>
  );
}
