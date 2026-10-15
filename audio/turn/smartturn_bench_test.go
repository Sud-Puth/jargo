package turn

import (
	"testing"
)

// BenchmarkNewSmartTurnV3_Dedicated measures creating new SmartTurnV3 analyzers with a dedicated session each time (Before).
func BenchmarkNewSmartTurnV3_Dedicated(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s, err := NewSmartTurnV3()
		if err != nil {
			b.Fatalf("NewSmartTurnV3: %v", err)
		}
		_ = s.Close()
	}
}

// BenchmarkNewSmartTurnV3_Shared measures creating new SmartTurnV3 analyzers reusing a shared SmartTurnModel (After).
func BenchmarkNewSmartTurnV3_Shared(b *testing.B) {
	model, err := NewSmartTurnModel()
	if err != nil {
		b.Fatalf("NewSmartTurnModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := NewSmartTurnV3WithModel(model)
		if err != nil {
			b.Fatalf("NewSmartTurnV3WithModel: %v", err)
		}
		_ = s.Close()
	}
}

// BenchmarkSmartTurnV3_Inference_Dedicated measures endpoint inference latency with a dedicated session.
func BenchmarkSmartTurnV3_Inference_Dedicated(b *testing.B) {
	s, err := NewSmartTurnV3()
	if err != nil {
		b.Fatalf("NewSmartTurnV3: %v", err)
	}
	defer func() { _ = s.Close() }()

	features := computeLogMel(refAudio())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.runModel(features)
	}
}

// BenchmarkSmartTurnV3_Inference_Shared measures endpoint inference latency using a shared model.
func BenchmarkSmartTurnV3_Inference_Shared(b *testing.B) {
	model, err := NewSmartTurnModel()
	if err != nil {
		b.Fatalf("NewSmartTurnModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	s, err := NewSmartTurnV3WithModel(model)
	if err != nil {
		b.Fatalf("NewSmartTurnV3WithModel: %v", err)
	}
	defer func() { _ = s.Close() }()

	features := computeLogMel(refAudio())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.runModel(features)
	}
}

// BenchmarkSmartTurnV3_Concurrent_Shared measures throughput under parallel worker evaluation.
func BenchmarkSmartTurnV3_Concurrent_Shared(b *testing.B) {
	model, err := NewSmartTurnModel()
	if err != nil {
		b.Fatalf("NewSmartTurnModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	features := computeLogMel(refAudio())
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		s, err := NewSmartTurnV3WithModel(model)
		if err != nil {
			b.Fatalf("NewSmartTurnV3WithModel: %v", err)
		}
		defer func() { _ = s.Close() }()

		for pb.Next() {
			_, _ = s.runModel(features)
		}
	})
}
