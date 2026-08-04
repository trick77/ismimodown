//go:build smoke

// Command probesmoke runs one real probe against MiMo and prints the result.
//
// Behind the `smoke` build tag so it never compiles into the normal build or
// runs in CI: the plan is explicit that no test makes a real API call. This is
// the manual live check, run by hand with a real key.
//
//	go run -tags smoke ./cmd/probesmoke
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/trick77/mimostats/internal/config"
	"github.com/trick77/mimostats/internal/probe"
)

func main() {
	key := os.Getenv("BACKEND_MIMO_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "BACKEND_MIMO_API_KEY is required")
		os.Exit(1)
	}

	client := probe.NewClient(probe.Config{
		BaseURL:       config.DefaultBaseURL,
		APIKey:        key,
		UserAgent:     config.DefaultUserAgent,
		SystemPrompt:  config.DefaultSystemPrompt,
		DialTimeout:   10 * time.Second,
		HeaderTimeout: 60 * time.Second,
		TTFTTimeout:   150 * time.Second,
		IdleTimeout:   45 * time.Second,
		Timeout:       240 * time.Second,
	})

	pinger := probe.NewPinger(5 * time.Second)
	for _, target := range []struct{ name, host string }{
		{probe.TargetMimoSGP, config.DefaultMimoHost},
		{probe.TargetRefSGP, config.DefaultRefSGPHost},
	} {
		r := pinger.Ping(context.Background(), target.name, target.host)
		fmt.Printf("ping  %-10s %-38s ok=%-5v dns=%6.1fms connect=%7.1fms %s\n",
			r.Target, target.host, r.OK, r.DNSMs, r.ConnectMs, r.ErrorClass)
	}

	for _, model := range []string{"mimo-v2.5", "mimo-v2.5-pro"} {
		q := probe.Pick(0)
		res, err := client.Run(context.Background(), probe.Request{
			ModelID: model, Probe: probe.ProbeInfer, Prompt: q.Prompt(),
			MaxTokens: probe.InferMaxTokens, QuestionID: q.ID, Assert: q.Assert,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "run %s: %v\n", model, err)
			continue
		}
		answerOK := "n/a"
		if res.AnswerOK != nil {
			answerOK = fmt.Sprintf("%v", *res.AnswerOK)
		}
		fmt.Printf("\ninfer %s  ok=%v class=%s\n", model, res.OK, res.ErrorClass)
		// ttfat-ttft printed at sub-millisecond resolution: in the healthy case
		// MiMo's role chunk and first content chunk arrive in the same batch, so
		// the gap is microseconds and %.0f would render it as zero.
		fmt.Printf("  ttft=%.3fms ttfat=%.3fms (gap=%.3fms) total=%.0fms itl_p50=%.1fms itl_p95=%.1fms tps=%.1f\n",
			res.TTFTMs, res.TTFATMs, res.TTFATMs-res.TTFTMs, res.TotalMs, res.ITLP50Ms, res.ITLP95Ms, res.OutputTPS)
		fmt.Printf("  trace: dns=%.1fms connect=%.1fms tls=%.1fms\n", res.DNSMs, res.ConnectMs, res.TLSMs)
		fmt.Printf("  tokens: prompt=%d output=%d cached=%d reasoning=%d  finish=%s\n",
			res.Usage.PromptTokens, res.Usage.CompletionTokens,
			res.Usage.PromptTokensDetails.CachedTokens,
			res.Usage.CompletionTokenDetails.ReasoningTokens, res.FinishReason)
		fmt.Printf("  question=%s answer_ok=%s\n", res.QuestionID, answerOK)
		fmt.Printf("  content: %.120s\n", res.Content)
	}

	// The wide probe, once, to confirm cache defeat actually holds.
	res, err := client.Run(context.Background(), probe.Request{
		ModelID: "mimo-v2.5", Probe: probe.ProbeWide,
		Prompt: probe.WidePrompt(), MaxTokens: probe.WideMaxTokens,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wide: %v\n", err)
		return
	}
	fmt.Printf("\nwide  mimo-v2.5  ok=%v class=%s\n", res.OK, res.ErrorClass)
	fmt.Printf("  ttft=%.0fms total=%.0fms itl_p50=%.1fms\n", res.TTFTMs, res.TotalMs, res.ITLP50Ms)
	fmt.Printf("  tokens: prompt=%d output=%d cached=%d reasoning=%d  finish=%s\n",
		res.Usage.PromptTokens, res.Usage.CompletionTokens,
		res.Usage.PromptTokensDetails.CachedTokens,
		res.Usage.CompletionTokenDetails.ReasoningTokens, res.FinishReason)
}
