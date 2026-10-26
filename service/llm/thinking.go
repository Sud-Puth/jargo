package llm

import "log/slog"

// ReportThinkingDefault reports, once per model, the thinking default a service
// sends a model whose request configures no thinking of its own.
//
// Services that send each model its lowest thinking setting call it whenever
// they apply that default, or find they have none for the model. The first
// request for each model logs the setting sent or, for a model that can think
// but has no default, warns that it runs at the provider's default, which can
// think before replying and slow the responses down.
//
// setting is the default as it is sent, such as `thinking={"type":"disabled"}`,
// or empty when the service lists none for the model. canThink decides whether a
// model with no default is warned about. provider names the provider, and
// settings the configuration that sets thinking, such as "Thinking or Effort in
// anthropic.Config", for the log lines.
func (b *Base) ReportThinkingDefault(model, setting string, canThink bool, provider, settings string) {
	b.thinkingDefaultMu.Lock()
	if model == b.loggedThinkingDefaultModel {
		b.thinkingDefaultMu.Unlock()
		return
	}
	b.loggedThinkingDefaultModel = model
	b.thinkingDefaultMu.Unlock()

	switch {
	case setting != "":
		slog.Info("sending the model its lowest thinking setting, for the fastest responses; "+
			"if the bot needs multistep reasoning, configure it",
			"service", b.Name(), "model", model, "setting", setting, "configure", settings)
	case canThink:
		slog.Warn("no thinking default for the model, so it runs at the provider's default, "+
			"which can think before replying and slow responses; for the fastest responses, "+
			"configure the lowest setting the model supports",
			"service", b.Name(), "model", model, "provider", provider, "configure", settings)
	}
}
