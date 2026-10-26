// Package capabilities describes what a bot does in a session, reported so
// clients can configure themselves.
//
// A pipeline.Worker derives a BotCapabilities from its pipeline's transports
// and parameters, and RTVI sends it to the client in the bot-ready message.
package capabilities

// BotCapabilities is what a bot does in a session.
//
// Every field is optional: nil means unknown, and a client should keep its
// default behavior for it. False means the bot does not do it, so a client can
// hide the matching UI.
type BotCapabilities struct {
	// AudioIn is whether the bot receives the user's audio.
	AudioIn *bool `json:"audio_in,omitempty"`
	// AudioOut is whether the bot sends audio to the user.
	AudioOut *bool `json:"audio_out,omitempty"`
	// VideoIn is whether the bot receives the user's video.
	VideoIn *bool `json:"video_in,omitempty"`
	// ScreenIn is whether the bot receives the user's screen share.
	ScreenIn *bool `json:"screen_in,omitempty"`
	// VideoOut is whether the bot sends video to the user.
	VideoOut *bool `json:"video_out,omitempty"`
	// Metrics is whether the bot reports metrics.
	Metrics *bool `json:"metrics,omitempty"`
}

// fields are the capabilities, in order, for treating them alike.
func (c *BotCapabilities) fields() []**bool {
	return []**bool{&c.AudioIn, &c.AudioOut, &c.VideoIn, &c.ScreenIn, &c.VideoOut, &c.Metrics}
}

// Combine combines the capabilities of two parts of the same bot.
//
// A field is true if either side is true, false if either side is false and
// neither is true, and nil if both are unknown.
func (c BotCapabilities) Combine(other BotCapabilities) BotCapabilities {
	var combined BotCapabilities
	mine, theirs, out := c.fields(), other.fields(), combined.fields()
	for i := range out {
		a, b := *mine[i], *theirs[i]
		switch {
		case a == nil && b == nil:
		case a == nil:
			*out[i] = new(*b)
		case b == nil:
			*out[i] = new(*a)
		default:
			*out[i] = new(*a || *b)
		}
	}
	return combined
}

// Override applies the fields other knows on top of these capabilities.
func (c BotCapabilities) Override(other BotCapabilities) BotCapabilities {
	result := c
	theirs, out := other.fields(), result.fields()
	for i := range out {
		if v := *theirs[i]; v != nil {
			*out[i] = new(*v)
		}
	}
	return result
}
