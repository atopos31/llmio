package balancers

import (
	"testing"
)

func TestLotteryPopEmpty(t *testing.T) {
	w := NewLottery(map[uint]int{})
	if _, err := w.Pop(); err == nil {
		t.Fatalf("expected error on empty set")
	}
}

func TestLotteryPopZeroTotal(t *testing.T) {
	w := NewLottery(map[uint]int{1: 0, 2: 0})
	if _, err := w.Pop(); err == nil {
		t.Fatalf("expected error when total weight is zero")
	}
}

func TestLotteryPopSingle(t *testing.T) {
	w := NewLottery(map[uint]int{5: 3})
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

func TestLotteryDelete(t *testing.T) {
	w := NewLottery(map[uint]int{1: 1})
	w.Delete(1)
	if _, err := w.Pop(); err == nil {
		t.Fatalf("expected error after deleting the only key")
	}
}

func TestRotor(t *testing.T) {
	t.Run("NewRotor", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
			3: 30,
		}
		wl := NewRotor(items)

		if wl.Len() != 3 {
			t.Errorf("Expected length 3, got %d", wl.Len())
		}
	})

	t.Run("Pop", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
			3: 30,
		}
		wl := NewRotor(items)

		// Should return the item with highest weight (3)
		result, err := wl.Pop()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if result != 3 {
			t.Errorf("Expected item 3 (highest weight), got %d", result)
		}
	})

	t.Run("Pop empty list", func(t *testing.T) {
		wl := NewRotor(map[uint]int{})
		_, err := wl.Pop()
		if err == nil {
			t.Error("Expected error when popping from empty list")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
			3: 30,
		}
		wl := NewRotor(items)

		wl.Delete(2)
		if wl.Len() != 2 {
			t.Errorf("Expected length 2 after deletion, got %d", wl.Len())
		}

		// Verify item 2 is no longer accessible
		found := false
		for e := wl.Front(); e != nil; e = e.Next() {
			if e.Value.(uint) == 2 {
				found = true
				break
			}
		}
		if found {
			t.Error("Item 2 should have been deleted")
		}
	})

	t.Run("Delete non-existent item", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
		}
		wl := NewRotor(items)

		originalLen := wl.Len()
		wl.Delete(999) // Non-existent item

		if wl.Len() != originalLen {
			t.Error("Deleting non-existent item should not change list length")
		}
	})

	t.Run("Reduce", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
			3: 30,
		}
		wl := NewRotor(items)

		// Reduce item 3 (highest weight)
		wl.Reduce(3)

		// Item 3 should now be at the back
		last := wl.Back()
		if last.Value.(uint) != 3 {
			t.Errorf("Expected item 3 to be moved to back after reduce, got %d", last.Value.(uint))
		}
	})

	t.Run("Reduce non-existent item", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
		}
		wl := NewRotor(items)

		originalLen := wl.Len()
		wl.Reduce(999) // Non-existent item

		if wl.Len() != originalLen {
			t.Error("Reducing non-existent item should not change list length")
		}
	})

	t.Run("Multiple operations", func(t *testing.T) {
		items := map[uint]int{
			1: 10,
			2: 20,
			3: 30,
			4: 40,
		}
		wl := NewRotor(items)

		// Initial state: [4, 3, 2, 1] (sorted by weight)

		// Pop should return 4 (but doesn't remove it from list)
		result, _ := wl.Pop()
		if result != 4 {
			t.Errorf("Expected 4, got %d", result)
		}

		// List is still [4, 3, 2, 1] after Pop

		// Reduce 3 (moves it to the back)
		wl.Reduce(3)

		// After reducing 3: [4, 2, 1, 3]

		// Next pop should return 4 (still highest)
		result, _ = wl.Pop()
		if result != 4 {
			t.Errorf("Expected 4, got %d", result)
		}

		// Delete 1
		wl.Delete(1)

		// After deleting 1: [4, 2, 3]

		// Remaining should be 3 items
		if wl.Len() != 3 {
			t.Errorf("Expected length 3, got %d", wl.Len())
		}

		// Next pop should return 4
		result, _ = wl.Pop()
		if result != 4 {
			t.Errorf("Expected 4, got %d", result)
		}
	})

	t.Run("Weight ordering", func(t *testing.T) {
		items := map[uint]int{
			1: 5,
			2: 15,
			3: 10,
			4: 20,
			5: 8,
		}
		wl := NewRotor(items)

		// Should be ordered: [4, 2, 3, 5, 1]
		expectedOrder := []uint{4, 2, 3, 5, 1}

		var actualOrder []uint
		for e := wl.Front(); e != nil; e = e.Next() {
			actualOrder = append(actualOrder, e.Value.(uint))
		}

		for i, expected := range expectedOrder {
			if actualOrder[i] != expected {
				t.Errorf("Position %d: expected %d, got %d", i, expected, actualOrder[i])
			}
		}
	})
}

func BenchmarkLottery(b *testing.B) {
	items := map[uint]int{
		1: 10,
		2: 20,
		3: 30,
		4: 40,
		5: 50,
	}

	b.Run("Pop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := NewLottery(items)
			w.Pop()
		}
	})

	b.Run("Delete", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := NewLottery(items)
			w.Delete(3)
		}
	})

	b.Run("Reduce", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := NewLottery(items)
			w.Reduce(3)
		}
	})
}

func BenchmarkRotor(b *testing.B) {
	items := map[uint]int{
		1: 10,
		2: 20,
		3: 30,
		4: 40,
		5: 50,
	}

	b.Run("Pop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			wl := NewRotor(items)
			wl.Pop()
		}
	})

	b.Run("Delete", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			wl := NewRotor(items)
			wl.Delete(3)
		}
	})

	b.Run("Reduce", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			wl := NewRotor(items)
			wl.Reduce(3)
		}
	})
}
