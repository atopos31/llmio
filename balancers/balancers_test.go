package balancers

import "testing"

func TestWeightedRandomPopEmpty(t *testing.T) {
	w := WeightedRandom{}
	if _, err := w.Pop(); err == nil {
		t.Fatalf("expected error on empty set")
	}
}

func TestWeightedRandomPopZeroTotal(t *testing.T) {
	w := WeightedRandom{1: 0, 2: 0}
	if _, err := w.Pop(); err == nil {
		t.Fatalf("expected error when total weight is zero")
	}
}

func TestWeightedRandomPopSingle(t *testing.T) {
	w := WeightedRandom{5: 3}
	for i := 0; i < 5; i++ {
		id, err := w.Pop()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != 5 {
			t.Fatalf("expected id 5, got %d", id)
		}
	}
}

func TestWeightedRandomDelete(t *testing.T) {
	w := WeightedRandom{1: 1}
	w.Delete(1)
	if _, ok := w[1]; ok {
		t.Fatalf("expected key 1 to be removed")
	}
}
