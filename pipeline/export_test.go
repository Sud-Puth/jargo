package pipeline

import "slices"

// UnusableProcessors returns the ids of the processors the unusable policy has
// been applied to, in ascending order.
func (t *Worker) UnusableProcessors() []uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]uint64, 0, len(t.unusable))
	for id := range t.unusable {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
