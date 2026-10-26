package transport_test

import (
	"reflect"
	"testing"

	"github.com/gojargo/jargo/pipeline"
	"github.com/gojargo/jargo/pipeline/capabilities"
	"github.com/gojargo/jargo/processor"
	"github.com/gojargo/jargo/transport"
)

func capsTransport(audioIn, audioOut bool) (*fakeInput, *fakeOutput) {
	p := transport.Params{AudioInEnabled: audioIn, AudioOutEnabled: audioOut}
	return newFakeInput(p, 0), newFakeOutput(p)
}

func workerOf(t *testing.T, cfg pipeline.WorkerConfig, procs ...processor.Processor) *pipeline.Worker {
	t.Helper()
	off := false
	cfg.EnableRTVI = &off
	return pipeline.NewWorker(pipeline.New(procs...), cfg)
}

func TestCapabilitiesAreDerivedFromTransportParams(t *testing.T) {
	in, out := capsTransport(true, true)
	worker := workerOf(t, pipeline.WorkerConfig{}, in, out)

	want := capabilities.BotCapabilities{
		AudioIn: new(true), AudioOut: new(true), VideoIn: new(false), ScreenIn: new(false),
		VideoOut: new(false), Metrics: new(false),
	}
	if got := worker.Capabilities(); !reflect.DeepEqual(got, want) {
		t.Errorf("capabilities %+v, want %+v", got, want)
	}
}

func TestCapabilitiesReportMetricsFromThePipelineParams(t *testing.T) {
	in, out := capsTransport(false, false)
	worker := workerOf(t, pipeline.WorkerConfig{Params: pipeline.Params{EnableMetrics: true}}, in, out)

	if m := worker.Capabilities().Metrics; m == nil || !*m {
		t.Errorf("metrics = %v, want true", m)
	}
}

func TestCapabilitiesOfTransportsNestedInAParallelPipeline(t *testing.T) {
	in, out := capsTransport(true, true)
	par, err := pipeline.NewParallel([]processor.Processor{in}, []processor.Processor{out})
	if err != nil {
		t.Fatal(err)
	}
	caps := workerOf(t, pipeline.WorkerConfig{}, par).Capabilities()

	if caps.AudioIn == nil || !*caps.AudioIn || caps.AudioOut == nil || !*caps.AudioOut {
		t.Errorf("capabilities %+v, want audio both ways", caps)
	}
}

func TestNoTransportLeavesMediaUnnew(t *testing.T) {
	want := capabilities.BotCapabilities{Metrics: new(false)}
	if got := workerOf(t, pipeline.WorkerConfig{}).Capabilities(); !reflect.DeepEqual(got, want) {
		t.Errorf("capabilities %+v, want %+v", got, want)
	}
}

func TestCapabilitiesGivenToTheWorkerOverrideTheDerivedOnes(t *testing.T) {
	in, out := capsTransport(true, true)
	worker := workerOf(t, pipeline.WorkerConfig{
		Capabilities: capabilities.BotCapabilities{VideoOut: new(true)},
	}, in, out)

	caps := worker.Capabilities()
	if caps.AudioIn == nil || !*caps.AudioIn || caps.VideoOut == nil || !*caps.VideoOut {
		t.Errorf("capabilities %+v, want audio in and video out", caps)
	}
}
