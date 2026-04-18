package messytodo

import (
	"testing"
	"time"
	"strings"
)

func TestTodoList_Add(t *testing.T) {
	// Setup a fresh list for testing
	list := NewTodoList()

	// --- Test Case 1: Normal usage (Basic addition) ---
	t.Run("NormalAddition", func(t *testing.T) {
		title := "Buy groceries"
		priority := Medium
		tags := []string{"errand", "food"}
		todo := list.Add(title, priority, tags)

		if todo.Title != title {
			t.Errorf("Expected title %q, got %q", title, todo.Title)
		}
		if todo.Priority != priority {
			t.Errorf("Expected priority %v, got %v", priority, todo.Priority)
		}
		if !reflect.DeepEqual(todo.Tags, tags) {
			t.Errorf("Expected tags %v, got %v", tags, todo.Tags)
		}
		if todo.Done != false {
			t.Errorf("Expected Done to be false, got true")
		}
		if todo.ID != 1 {
			t.Errorf("Expected ID 1, got %d", todo.ID)
		}
		// Check if nextID was incremented
		if list.nextID != 2 {
			t.Errorf("Expected nextID 2, got %d", list.nextID)
		}
	})

	// --- Test Case 2: Edge case - Zero values (Empty title, Low priority, Empty tags) ---
	t.Run("ZeroValues", func(t *testing.T) {
		// Reset list state for predictable ID testing
		list = NewTodoList()
		// Add one item first to ensure ID starts correctly
		list.Add("Initial Item", Low, nil)

		title := ""
		priority := Low
		tags := []string{}
		todo := list.Add(title, priority, tags)

		if todo.Title != "" {
			t.Errorf("Expected empty title, got %q", todo.Title)
		}
		if todo.Priority != Low {
			t.Errorf("Expected Low priority, got %v", todo.Priority)
		}
		if len(todo.Tags) != 0 {
			t.Errorf("Expected empty tags slice, got %v", todo.Tags)
		}
		if todo.ID != 2 {
			t.Errorf("Expected ID 2, got %d", todo.ID)
		}
	})

	// --- Test Case 3: Edge case - Duplicate inputs (Should still work, but check ID sequence) ---
	t.Run("DuplicateInputs", func(t *testing.T) {
		// Add two items with identical content
		list = NewTodoList()
		todo1 := list.Add("Duplicate Test", Medium, []string{"a"})
		todo2 := list.Add("Duplicate Test", Medium, []string{"a"})

		if todo1.ID != 1 || todo2.ID != 2 {
			t.Errorf("Expected IDs 1 and 2, got %d and %d", todo1.ID, todo2.ID)
		}
		if todo1.Title != "Duplicate Test" || todo2.Title != "Duplicate Test" {
			t.Errorf("Titles mismatch")
		}
	})

	// --- Test Case 4: Case sensitivity (Title and Tags) ---
	t.Run("CaseSensitivity", func(t *testing.T) {
		list = NewTodoList()
		// Add item with mixed case
		title := "Case Test Item"
		tags := []string{"TagA", "tagb"}
		todo := list.Add(title, Medium, tags)

		if todo.Title != title {
			t.Errorf("Title case mismatch")
		}
		// Check if the tags array preserves case
		expectedTags := []string{"TagA", "tagb"}
		if !reflect.DeepEqual(todo.Tags, expectedTags) {
			t.Errorf("Tags case mismatch. Expected %v, got %v", expectedTags, todo.Tags)
		}
	})

	// --- Test Case 5: Time edge cases (Time.Now() dependency) ---
	t.Run("TimeHandling", func(t *testing.T) {
		// We cannot perfectly test time.Now(), but we can check that the created time is set.
		list = NewTodoList()
		// Capture the time before adding the item
		startTime := time.Now()
		todo := list.Add("Time Check", Low, nil)

		// Check that CreatedAt is close to the start time (allowing for minor execution delay)
		if todo.CreatedAt.Before(startTime.Add(-1 * time.Second)) || todo.CreatedAt.After(startTime.Add(1*time.Second)) {
			t.Errorf("CreatedAt time seems incorrect. Expected near %v, got %v", startTime, todo.CreatedAt)
		}
	})

	// --- Test Case 6: Multiple additions to verify ID sequence ---
	t.Run("SequentialAddition", func(t *testing.T) {
		list = NewTodoList()
		t1 := list.Add("First", Low, nil)
		t2 := list.Add("Second", Medium, nil)
		t3 := list.Add("Third", High, nil)

		if t1.ID != 1 || t2.ID != 2 || t3.ID != 3 {
			t.Errorf("ID sequence failed. Expected 1, 2, 3. Got %d, %d, %d", t1.ID, t2.ID, t3.ID)
		}
		if list.nextID != 4 {
			t.Errorf("nextID failed. Expected 4, got %d", list.nextID)
		}
	})
}

func TestCompleteAndReopen(t *testing.T) {
	// Setup: Create a fresh todo list
	tl := NewTodoList()

	// Add initial items
	todo1 := tl.Add("Task 1", Low, []string{"work"}) // ID 1
	todo2 := tl.Add("Task 2", Medium, []string{"personal"}) // ID 2
	todo3 := tl.Add("Task 3", High, []string{"work"}) // ID 3

	// --- Test Complete Method ---

	t.Run("Complete_NormalUsage", func(t *testing.T) {
		err := tl.Complete(todo2.ID)
		if err != nil {
			t.Fatalf("Expected no error when completing existing todo, got %v", err)
		}
		// Verify state change
		completedTodo, _ := tl.Get(todo2.ID)
		if !completedTodo.Done {
			t.Errorf("Expected todo %d to be marked as done, but it is not", todo2.ID)
		}
	})

	t.Run("Complete_NonExistentID", func(t *testing.T) {
		err := tl.Complete(999)
		if err == nil {
			t.Fatal("Expected an error for non-existent ID, but got nil")
		}
		expectedError := "todo 999 not found"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("Complete_AlreadyCompleted", func(t *testing.T) {
		// Complete todo1 first
		tl.Complete(todo1.ID)
		// Re-run complete on the same ID (should succeed without error)
		err := tl.Complete(todo1.ID)
		if err != nil {
			t.Fatalf("Expected no error when completing already completed todo, got %v", err)
		}
		// Check state remains done
		completedTodo, _ := tl.Get(todo1.ID)
		if !completedTodo.Done {
			t.Errorf("Expected todo %d to remain done", todo1.ID)
		}
	})

	// --- Test Reopen Method ---

	t.Run("Reopen_NormalUsage", func(t *testing.T) {
		// 1. Complete todo1 first
		tl.Complete(todo1.ID)
		// 2. Reopen it
		err := tl.Reopen(todo1.ID)
		if err != nil {
			t.Fatalf("Expected no error when reopening existing todo, got %v", err)
		}
		// Verify state change
		reopenedTodo, _ := tl.Get(todo1.ID)
		if reopenedTodo.Done {
			t.Errorf("Expected todo %d to be marked as pending, but it is still done", todo1.ID)
		}
	})

	t.Run("Reopen_NonExistentID", func(t *testing.T) {
		err := tl.Reopen(999)
		if err == nil {
			t.Fatal("Expected an error for non-existent ID, but got nil")
		}
		expectedError := "todo 999 not found"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("Reopen_AlreadyPending", func(t *testing.T) {
		// Ensure todo2 is pending initially
		todo2.Done = false
		// Reopen it (should succeed without error)
		err := tl.Reopen(todo2.ID)
		if err != nil {
			t.Fatalf("Expected no error when reopening already pending todo, got %v", err)
		}
		// Check state remains pending
		pendingTodo, _ := tl.Get(todo2.ID)
		if pendingTodo.Done {
			t.Errorf("Expected todo %d to remain pending", todo2.ID)
		}
	})

	// --- Edge Case: Empty List ---
	t.Run("Complete_EmptyList", func(t *testing.T) {
		emptyTL := NewTodoList()
		err := emptyTL.Complete(1)
		if err == nil {
			t.Fatal("Expected error when completing on an empty list, but got nil")
		}
	})

	t.Run("Reopen_EmptyList", func(t *testing.T) {
		emptyTL := NewTodoList()
		err := emptyTL.Reopen(1)
		if err == nil {
			t.Fatal("Expected error when reopening on an empty list, but got nil")
		}
	})

	// --- Edge Case: Time/State Interaction (Implicitly tested by state checks above) ---
	// Since Complete/Reopen only toggle the 'Done' boolean, time edge cases are not directly applicable
	// unless we were testing methods that *use* the Done status (like Overdue or Pending).
}

func TestDelete(t *testing.T) {
	// Setup: Create a fresh todo list
	tl := NewTodoList()

	// Add initial items
	todo1 := tl.Add("Task 1", Low, []string{"work"}) // ID 1
	todo2 := tl.Add("Task 2", Medium, []string{"home"}) // ID 2
	todo3 := tl.Add("Task 3", High, []string{"work", "urgent"}) // ID 3
	todo4 := tl.Add("Task 4", Low, []string{}) // ID 4

	// --- Test Case 1: Normal deletion (middle item) ---
	t.Run("DeleteMiddleItem", func(t *testing.T) {
		initialCount := len(tl.todos)
		err := tl.Delete(todo2.ID) // Delete Task 2 (ID 2)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		// Check if the item was removed
		_, err = tl.Get(todo2.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted item ID %d, but got nil", todo2.ID)
		}

		// Check if the list size decreased correctly
		if len(tl.todos) != initialCount-1 {
			t.Errorf("Expected list size to be %d, got %d", initialCount-1, len(tl.todos))
		}

		// Check if subsequent IDs are still present (Task 3 should still exist)
		_, err = tl.Get(todo3.ID)
		if err != nil {
			t.Errorf("Expected to find item ID %d after deletion, got error: %v", todo3.ID, err)
		}
	})

	// Re-setup for subsequent tests to ensure clean state (since Delete modifies the slice)
	tl = NewTodoList()
	todo1 = tl.Add("Task 1", Low, []string{"work"}) // ID 1
	todo2 = tl.Add("Task 2", Medium, []string{"home"}) // ID 2
	todo3 = tl.Add("Task 3", High, []string{"work", "urgent"}) // ID 3
	todo4 = tl.Add("Task 4", Low, []string{}) // ID 4

	// --- Test Case 2: Edge Case - Deleting the first item ---
	t.Run("DeleteFirstItem", func(t *testing.T) {
		initialCount := len(tl.todos)
		err := tl.Delete(todo1.ID) // Delete Task 1 (ID 1)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		// Check if the item was removed
		_, err = tl.Get(todo1.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted item ID %d, but got nil", todo1.ID)
		}

		// Check if the list size decreased correctly
		if len(tl.todos) != initialCount-1 {
			t.Errorf("Expected list size to be %d, got %d", initialCount-1, len(tl.todos))
		}

		// Check if the next item (ID 2) is still present
		_, err = tl.Get(todo2.ID)
		if err != nil {
			t.Errorf("Expected to find item ID %d after deleting first item, got error: %v", todo2.ID, err)
		}
	})

	// Re-setup
	tl = NewTodoList()
	todo1 = tl.Add("Task 1", Low, []string{"work"}) // ID 1
	todo2 = tl.Add("Task 2", Medium, []string{"home"}) // ID 2
	todo3 = tl.Add("Task 3", High, []string{"work", "urgent"}) // ID 3
	todo4 = tl.Add("Task 4", Low, []string{}) // ID 4

	// --- Test Case 3: Edge Case - Deleting the last item ---
	t.Run("DeleteLastItem", func(t *testing.T) {
		initialCount := len(tl.todos)
		err := tl.Delete(todo4.ID) // Delete Task 4 (ID 4)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		// Check if the item was removed
		_, err = tl.Get(todo4.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted item ID %d, but got nil", todo4.ID)
		}

		// Check if the list size decreased correctly
		if len(tl.todos) != initialCount-1 {
			t.Errorf("Expected list size to be %d, got %d", initialCount-1, len(tl.todos))
		}

		// Check if the previous item (ID 3) is still present
		_, err = tl.Get(todo3.ID)
		if err != nil {
			t.Errorf("Expected to find item ID %d after deleting last item, got error: %v", todo3.ID, err)
		}
	})

	// Re-setup
	tl = NewTodoList()
	todo1 = tl.Add("Task 1", Low, []string{"work"}) // ID 1
	todo2 = tl.Add("Task 2", Medium, []string{"home"}) // ID 2
	todo3 = tl.Add("Task 3", High, []string{"work", "urgent"}) // ID 3
	todo4 = tl.Add("Task 4", Low, []string{}) // ID 4

	// --- Test Case 4: Edge Case - Deleting non-existent ID ---
	t.Run("DeleteNonExistentItem", func(t *testing.T) {
		nonExistentID := 999
		err := tl.Delete(nonExistentID)
		if err == nil {
			t.Errorf("Expected an error for non-existent ID %d, but got nil", nonExistentID)
		}
		expectedErrMsg := fmt.Sprintf("todo %d not found", nonExistentID)
		if err != nil && err.Error() != expectedErrMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
		}
		// Ensure list size hasn't changed
		if len(tl.todos) != 4 {
			t.Errorf("Expected list size to remain 4, got %d", len(tl.todos))
		}
	})

	// --- Test Case 5: Edge Case - Empty list ---
	t.Run("DeleteFromEmptyList", func(t *testing.T) {
		emptyTl := NewTodoList()
		err := emptyTl.Delete(1)
		if err == nil {
			t.Errorf("Expected an error when deleting from an empty list, but got nil")
		}
	})

	// --- Test Case 6: Edge Case - Deleting the only item ---
	t.Run("DeleteOnlyItem", func(t *testing.T) {
		singleTl := NewTodoList()
		singleTodo := singleTl.Add("Single Task", Medium, nil) // ID 1
		initialCount := len(singleTl.todos)

		err := singleTl.Delete(singleTodo.ID)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		// Check if the item was removed
		_, err = singleTl.Get(singleTodo.ID)
		if err == nil {
			t.Errorf("Expected error when getting deleted item ID %d, but got nil", singleTodo.ID)
		}

		// Check if the list size is zero
		if len(singleTl.todos) != 0 {
			t.Errorf("Expected list size to be 0, got %d", len(singleTl.todos))
		}
	})

	// --- Test Case 7: Duplicate Inputs (Not applicable for Delete by ID, but testing robustness) ---
	// Since Delete uses ID, duplicate IDs are impossible in a correctly managed TodoList.
	// We rely on the ID uniqueness enforced by the Add method.
}

func TestGet(t *testing.T) {
	// Setup: Create a fresh todo list
	tl := NewTodoList()

	// Add initial items
	todo1 := tl.Add("Task 1", Low, []string{"work"})
	todo2 := tl.Add("Task 2", High, []string{"personal"})
	todo3 := tl.Add("Task 3", Medium, []string{"work", "urgent"})

	// --- Test Case 1: Normal usage (Get existing item) ---
	t.Run("GetExistingItem", func(t *testing.T) {
		retrievedTodo, err := tl.Get(todo2.ID)
		if err != nil {
			t.Fatalf("Expected no error, but got: %v", err)
		}
		if retrievedTodo == nil {
			t.Fatal("Expected a todo item, but got nil")
		}
		if retrievedTodo.ID != todo2.ID {
			t.Errorf("Expected ID %d, got %d", todo2.ID, retrievedTodo.ID)
		}
		if retrievedTodo.Title != todo2.Title {
			t.Errorf("Expected title %q, got %q", todo2.Title, retrievedTodo.Title)
		}
	})

	// --- Test Case 2: Edge case (Non-existent ID) ---
	t.Run("GetNonExistentItem", func(t *testing.T) {
		nonExistentID := 999
		_, err := tl.Get(nonExistentID)
		if err == nil {
			t.Errorf("Expected an error for non-existent ID %d, but got nil", nonExistentID)
		}
		expectedError := "todo 999 not found"
		if err != nil && err.Error() != expectedError {
			t.Errorf("Expected error message %q, got %q", expectedError, err.Error())
		}
	})

	// --- Test Case 3: Edge case (Zero ID) ---
	t.Run("GetZeroID", func(t *testing.T) {
		_, err := tl.Get(0)
		if err == nil {
			t.Errorf("Expected an error for ID 0, but got nil")
		}
		expectedError := "todo 0 not found"
		if err != nil && err.Error() != expectedError {
			t.Errorf("Expected error message %q, got %q", expectedError, err.Error())
		}
	})

	// --- Test Case 4: Edge case (Empty list) ---
	t.Run("GetFromEmptyList", func(t *testing.T) {
		emptyList := NewTodoList()
		_, err := emptyList.Get(1)
		if err == nil {
			t.Errorf("Expected an error when getting from an empty list, but got nil")
		}
	})

	// --- Test Case 5: State change verification (Completing an item) ---
	t.Run("GetAfterCompletion", func(t *testing.T) {
		// Complete todo1
		err := tl.Complete(todo1.ID)
		if err != nil {
			t.Fatalf("Failed to complete todo1: %v", err)
		}

		// Get it back and check the Done status
		retrievedTodo, err := tl.Get(todo1.ID)
		if err != nil {
			t.Fatalf("Failed to get todo1 after completion: %v", err)
		}
		if !retrievedTodo.Done {
			t.Errorf("Expected todo1 to be marked as done, but it was not")
		}
	})

	// --- Test Case 6: Duplicate inputs (Not applicable for Get, as it's by ID) ---
	// Since Get uses a unique ID, duplicate inputs are impossible unless the internal state is corrupted.
	// We rely on the ID mechanism ensuring uniqueness.

	// --- Test Case 7: Case sensitivity (Not applicable for Get, as it's by ID) ---
	// ID comparison is integer-based, so case sensitivity is not a concern.
}

func TestSetDueDateAndOverdue(t *testing.T) {
	// Setup: Create a fresh todo list
	tl := NewTodoList()

	// Helper function to create a todo item and capture its ID
	addTodo := func(title string, priority Priority, tags []string) *Todo {
		return tl.Add(title, priority, tags)
	}

	// --- Setup for Time Edge Cases ---
	// Use a fixed reference time for predictable testing, simulating 'now' for creation
	now := time.Date(2023, time.October, 27, 12, 0, 0, 0, time.UTC)
	// Manually set the creation time for the first item to control the 'now' reference for SetDueDate
	todo1 := addTodo("Task 1", Medium, nil)
	todo1.CreatedAt = now.Add(-24 * time.Hour) // Created yesterday

	// Create a second item for testing Overdue logic
	todo2 := addTodo("Task 2", Medium, nil)
	todo2.CreatedAt = now.Add(-1 * time.Hour) // Created 1 hour ago

	// Create a third item for testing Overdue logic
	todo3 := addTodo("Task 3", Medium, nil)
	todo3.CreatedAt = now.Add(-1 * time.Hour)

	// --- Test SetDueDate ---

	t.Run("SetDueDate_NormalUsage", func(t *testing.T) {
		// Set a due date in the future for todo1
		futureDate := now.Add(48 * time.Hour)
		err := tl.SetDueDate(todo1.ID, futureDate)
		if err != nil {
			t.Fatalf("Expected no error setting due date, got: %v", err)
		}
		// Verify the due date was set correctly
		todo, _ := tl.Get(todo1.ID)
		if !todo.DueDate.Equal(futureDate) {
			t.Errorf("Expected due date to be %v, got %v", futureDate, todo.DueDate)
		}
	})

	t.Run("SetDueDate_ErrorNotFound", func(t *testing.T) {
		// Try setting due date for a non-existent ID
		err := tl.SetDueDate(999, time.Now())
		if err == nil {
			t.Fatal("Expected an error for non-existent ID, got nil")
		}
		expectedError := "todo 999 not found"
		if err.Error() != expectedError {
			t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("SetDueDate_ErrorBeforeCreationDate", func(t *testing.T) {
		// Set a due date before the creation date of todo2
		pastDate := todo2.CreatedAt.Add(-1 * time.Hour)
		err := tl.SetDueDate(todo2.ID, pastDate)
		if err == nil {
			t.Fatal("Expected an error when due date is before creation date, got nil")
		}
		expectedError := "due date cannot be before creation date"
		if err.Error() != expectedError {
			t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("SetDueDate_EdgeCaseZeroTime", func(t *testing.T) {
		// Set due date to zero time (should fail if creation date is not zero)
		zeroTime := time.Time{}
		err := tl.SetDueDate(todo1.ID, zeroTime)
		// Since todo1.CreatedAt is not zero, setting it to zero time should trigger the error.
		if err == nil {
			t.Fatal("Expected error when setting due date to zero time (before creation), got nil")
		}
		expectedError := "due date cannot be before creation date"
		if err.Error() != expectedError {
			t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
		}
	})

	// --- Setup for Overdue Tests ---
	// Resetting the list state for clean Overdue testing
	tl = NewTodoList()
	// 1. Overdue item (Due yesterday)
	overdueTime := now.Add(-24 * time.Hour)
	todoOverdue := addTodo("Overdue Task", Medium, nil)
	todoOverdue.DueDate = overdueTime

	// 2. Due today (Should NOT be overdue unless time moves)
	dueTodayTime := now.Add(time.Minute) // Slightly in the future relative to 'now' in the test function
	todoDueToday := addTodo("Due Today Task", Medium, nil)
	todoDueToday.DueDate = dueTodayTime

	// 3. Future item (Due tomorrow)
	futureTime := now.Add(24 * time.Hour)
	todoFuture := addTodo("Future Task", Medium, nil)
	todoFuture.DueDate = futureTime

	// 4. Item with zero due date (Should not be considered overdue by default logic, but test robustness)
	todoZeroDue := addTodo("Zero Due Task", Medium, nil)
	todoZeroDue.DueDate = time.Time{} // Zero time

	// --- Test Overdue ---

	t.Run("Overdue_NormalUsage", func(t *testing.T) {
		// To reliably test Overdue, we must mock time.Now(). Since we cannot easily mock time.Now()
		// without external libraries, we will adjust the test logic: we will set the 'now' reference
		// to be exactly one second after the overdue item's due date, but before the dueToday item's due date.

		// Re-setup to control the 'now' reference point for the test run
		tl = NewTodoList()
		testNow := time.Date(2023, time.October, 27, 12, 0, 1, 0, time.UTC) // Set 'now' reference point
		
		// Item 1: Overdue (Due 1 second ago)
		todoOverdue = addTodo("Overdue Task", Medium, nil)
		todoOverdue.DueDate = testNow.Add(-2 * time.Second)

		// Item 2: Due Now/Future (Due 1 second from now)
		todoDueToday = addTodo("Due Today Task", Medium, nil)
		todoDueToday.DueDate = testNow.Add(1 * time.Second)

		// Item 3: Future (Due tomorrow)
		todoFuture = addTodo("Future Task", Medium, nil)
		todoFuture.DueDate = testNow.Add(24 * time.Hour)

		// Item 4: Zero Due Date (Should not be overdue)
		todoZeroDue = addTodo("Zero Due Task", Medium, nil)
		todoZeroDue.DueDate = time.Time{}

		// Temporarily override time.Now() behavior for this test block (conceptually)
		// Since we can't mock time.Now(), we rely on the fact that the test runner executes sequentially,
		// and we must assume the test environment's time.Now() will be *after* the setup time.
		// For this test, we will check if the overdue item is present, and the others are not.

		// NOTE: Due to the constraints of not using external mocking libraries, this test assumes
		// that the time.Now() called inside Overdue() will be *after* the setup time for the overdue item,
		// but *before* the dueToday item's due time.

		overdueList := tl.Overdue()

		if len(overdueList) != 1 {
			t.Fatalf("Expected exactly 1 overdue item, got %d", len(overdueList))
		}

		if overdueList[0].ID != todoOverdue.ID {
			t.Errorf("Expected overdue item ID %d, got ID %d", todoOverdue.ID, overdueList[0].ID)
		}
	})

	t.Run("Overdue_EdgeCaseEmptyList", func(t *testing.T) {
		emptyTl := NewTodoList()
		overdueList := emptyTl.Overdue()
		if len(overdueList) != 0 {
			t.Errorf("Expected empty list for empty todo list, got %d items", len(overdueList))
		}
	})

	t.Run("Overdue_EdgeCaseAllFuture", func(t *testing.T) {
		tl = NewTodoList()
		// Set all items far in the future
		futureTime := time.Now().Add(24 * time.Hour)
		todo1 := addTodo("Future 1", Medium, nil)
		todo1.DueDate = futureTime
		todo2 := addTodo("Future 2", Medium, nil)
		todo2.DueDate = futureTime.Add(time.Hour)

		overdueList := tl.Overdue()
		if len(overdueList) != 0 {
			t.Errorf("Expected 0 overdue items when all are in the future, got %d", len(overdueList))
		}
	})

	t.Run("Overdue_EdgeCaseAllPast", func(t *testing.T) {
		tl = NewTodoList()
		// Set all items in the past
		pastTime := time.Now().Add(-24 * time.Hour)
		todo1 := addTodo("Past 1", Medium, nil)
		todo1.DueDate = pastTime
		todo2 := addTodo("Past 2", Medium, nil)
		todo2.DueDate = pastTime.Add(-time.Hour)

		overdueList := tl.Overdue()
		if len(overdueList) != 2 {
			t.Errorf("Expected 2 overdue items when all are in the past, got %d", len(overdueList))
		}
	})
}

func TestByPriority(t *testing.T) {
	// Setup a fresh todo list
	tl := NewTodoList()

	// Helper function to create a todo item with specific properties
	addTodo := func(title string, priority Priority, done bool) *Todo {
		// Manually set ID and bypass time.Now() for predictable testing
		t := &Todo{
			ID:       tl.nextID,
			Title:    title,
			Done:     done,
			Priority: priority,
			CreatedAt: time.Now().Add(-time.Hour), // Fixed creation time
			DueDate:  time.Now().Add(-time.Hour),
			Tags:     []string{"test"},
		}
		tl.todos = append(tl.todos, t)
		tl.nextID++
		return t
	}

	// 1. Normal usage: Test filtering for High priority
	todoHigh1 := addTodo("High Task 1", High, false)
	todoHigh2 := addTodo("High Task 2", High, false)
	todoMedium := addTodo("Medium Task", Medium, false)
	todoLow := addTodo("Low Task", Low, false)

	// 2. Edge case: Test filtering for a priority that only has one item
	todoSingle := addTodo("Single Task", Medium, false)

	// 3. Edge case: Test filtering when some items are completed (should be excluded)
	todoHighDone := addTodo("High Done Task", High, true)
	todoMediumDone := addTodo("Medium Done Task", Medium, true)

	// 4. Edge case: Test filtering with zero/empty inputs (though Priority is an enum, we test the logic)
	// We will test the zero value (Low) explicitly.
	// We will test the case where no items match the criteria.

	// --- Test Case 1: Normal filtering (High) ---
	t.Run("Normal_HighPriority", func(t *testing.T) {
		results := tl.ByPriority(High)
		if len(results) != 2 {
			t.Errorf("Expected 2 High priority tasks, got %d", len(results))
		}
		// Check if the correct items are present (by ID)
		ids := make(map[int]bool)
		for _, t := range results {
			ids[t.ID] = true
		}
		if !ids[todoHigh1.ID] || !ids[todoHigh2.ID] {
			t.Errorf("Did not find both expected High priority tasks in results.")
		}
	})

	// --- Test Case 2: Normal filtering (Medium) ---
	t.Run("Normal_MediumPriority", func(t *testing.T) {
		results := tl.ByPriority(Medium)
		// Should include todoMedium and todoSingle, but exclude todoMediumDone
		if len(results) != 2 {
			t.Errorf("Expected 2 Medium priority tasks, got %d", len(results))
		}
	})

	// --- Test Case 3: Edge Case - Zero Value (Low) ---
	t.Run("EdgeCase_LowPriority", func(t *testing.T) {
		results := tl.ByPriority(Low)
		if len(results) != 1 {
			t.Errorf("Expected 1 Low priority task, got %d", len(results))
		}
	})

	// --- Test Case 4: Edge Case - All items are done ---
	t.Run("EdgeCase_AllDone", func(t *testing.T) {
		// Reset list to only contain done items
		tl = NewTodoList()
		todoDone1 := addTodo("Done 1", High, true)
		todoDone2 := addTodo("Done 2", Medium, true)

		results := tl.ByPriority(High)
		if len(results) != 0 {
			t.Errorf("Expected 0 results when all items are done, got %d", len(results))
		}
	})

	// --- Test Case 5: Edge Case - No items match the criteria ---
	t.Run("EdgeCase_NoMatch", func(t *testing.T) {
		// Add one item of a specific priority
		tl = NewTodoList()
		addTodo("Test", High, false)

		// Try to filter by a priority that wasn't added (conceptually, though we use the enum)
		// Since we can't easily create an unassigned priority, we test filtering for a priority
		// that we know has no *pending* items.
		// We will temporarily set the list to only contain done items and test for High.
		tl.todos = []*Todo{
			{ID: 1, Title: "Done", Done: true, Priority: High},
			{ID: 2, Title: "Done", Done: true, Priority: Medium},
		}
		results := tl.ByPriority(High)
		if len(results) != 0 {
			t.Errorf("Expected 0 results when no pending items match priority, got %d", len(results))
		}
	})

	// --- Test Case 6: Duplicate inputs (Not applicable for ByPriority, as it filters existing state) ---
	// The function operates on the internal slice, so duplicate *inputs* are handled by the list structure.
	// We test if the function correctly handles multiple items with the same properties.
	t.Run("Duplicate_MultipleMatches", func(t *testing.T) {
		tl = NewTodoList()
		// Add two distinct items that both match High priority and are pending
		todoA := addTodo("A", High, false)
		todoB := addTodo("B", High, false)

		results := tl.ByPriority(High)
		if len(results) != 2 {
			t.Errorf("Expected 2 results for duplicate matches, got %d", len(results))
		}
		// Ensure both unique IDs are returned
		ids := make(map[int]bool)
		for _, t := range results {
			ids[t.ID] = true
		}
		if !ids[todoA.ID] || !ids[todoB.ID] {
			t.Errorf("Did not find both expected unique items.")
		}
	})

	// --- Test Case 7: Case Sensitivity (Not applicable for ByPriority, as it compares enums) ---
	// This test confirms that the comparison is strictly based on the Priority enum value.
	t.Run("CaseSensitivity_EnumCheck", func(t *testing.T) {
		tl = NewTodoList()
		// Add one item explicitly set to Medium
		todoM := addTodo("Medium Test", Medium, false)
		// Manually add an item that *looks* like High but is set to Medium
		todoFakeH := &Todo{ID: 2, Title: "Fake High", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now()}
		tl.todos = append(tl.todos, todoFakeH)

		results := tl.ByPriority(High)
		if len(results) != 0 {
			t.Errorf("Should not find any High priority items if none were added correctly, got %d", len(results))
		}

		results = tl.ByPriority(Medium)
		if len(results) != 2 {
			t.Errorf("Expected 2 Medium priority items, got %d", len(results))
		}
	})
}

func TestByTag(t *testing.T) {
	// Setup a fresh todo list
	tl := NewTodoList()

	// Helper function to create a todo item with specific tags
	addTodo := func(title string, tags []string) *Todo {
		// We need to bypass the time.Now() dependency for predictable testing,
		// but since we can't modify the struct/methods, we'll just add items
		// and rely on the fact that the IDs will be sequential.
		t := &Todo{
			ID:       tl.nextID,
			Title:    title,
			Done:     false,
			Priority: Low,
			CreatedAt: time.Now(),
			Tags:     tags,
		}
		tl.todos = append(tl.todos, t)
		tl.nextID++
		return t
	}

	// --- Test Case 1: Normal usage - Single tag match ---
	t.Run("SingleTagMatch", func(t *testing.T) {
		// Reset list state for clean test run
		tl = NewTodoList()
		todo1 := addTodo("Task A", []string{"work", "urgent"})
		todo2 := addTodo("Task B", []string{"personal", "groceries"})
		todo3 := addTodo("Task C", []string{"work", "home"})

		results := tl.ByTag("work")
		if len(results) != 2 {
			t.Errorf("Expected 2 results for tag 'work', got %d", len(results))
		}
		// Check if the correct items are present (by ID)
		foundIDs := make(map[int]bool)
		for _, t := range results {
			foundIDs[t.ID] = true
		}
		if !foundIDs[todo1.ID] || !foundIDs[todo3.ID] {
			t.Errorf("Did not find expected todos for tag 'work'. Found IDs: %v", foundIDs)
		}
	})

	// --- Test Case 2: Normal usage - Multiple tags on one item ---
	t.Run("MultipleTagsOnOneItem", func(t *testing.T) {
		tl = NewTodoList()
		todo1 := addTodo("Task A", []string{"work", "urgent", "client"})
		todo2 := addTodo("Task B", []string{"personal", "groceries"})

		results := tl.ByTag("client")
		if len(results) != 1 {
			t.Errorf("Expected 1 result for tag 'client', got %d", len(results))
		}
		if results[0].ID != todo1.ID {
			t.Errorf("Expected result ID %d, got %d", todo1.ID, results[0].ID)
		}
	})

	// --- Test Case 3: Edge Case - No matching tags ---
	t.Run("NoMatchingTags", func(t *testing.T) {
		tl = NewTodoList()
		addTodo("Task A", []string{"work"})
		addTodo("Task B", []string{"personal"})

		results := tl.ByTag("nonexistent")
		if len(results) != 0 {
			t.Errorf("Expected 0 results for tag 'nonexistent', got %d", len(results))
		}
	})

	// --- Test Case 4: Edge Case - Empty input list ---
	t.Run("EmptyList", func(t *testing.T) {
		tl = NewTodoList()
		results := tl.ByTag("any")
		if len(results) != 0 {
			t.Errorf("Expected 0 results on an empty list, got %d", len(results))
		}
	})

	// --- Test Case 5: Edge Case - Empty tag string input ---
	t.Run("EmptyTagInput", func(t *testing.T) {
		tl = NewTodoList()
		addTodo("Task A", []string{"work"})
		addTodo("Task B", []string{}) // Item with no tags

		// The implementation checks if tt == tag. If tag is "", it will only match
		// if an item explicitly has an empty string tag, which is unlikely/bad practice.
		// We test for a tag that doesn't exist.
		results := tl.ByTag("")
		if len(results) != 0 {
			t.Errorf("Expected 0 results for empty tag input, got %d", len(results))
		}
	})

	// --- Test Case 6: Case Sensitivity ---
	t.Run("CaseSensitivity", func(t *testing.T) {
		tl = NewTodoList()
		todo1 := addTodo("Task A", []string{"Work"})
		todo2 := addTodo("Task B", []string{"work"})

		// Should only find the exact case match
		results := tl.ByTag("work")
		if len(results) != 1 || results[0].ID != todo2.ID {
			t.Errorf("Expected only the lowercase match, got %d results", len(results))
		}

		results = tl.ByTag("Work")
		if len(results) != 1 || results[0].ID != todo1.ID {
			t.Errorf("Expected only the capitalized match, got %d results", len(results))
		}
	})

	// --- Test Case 7: Duplicate tags (Should still work correctly) ---
	t.Run("DuplicateTagsInItem", func(t *testing.T) {
		tl = NewTodoList()
		// Although the struct allows duplicate tags, the logic iterates over the slice.
		// If the input tags are ["a", "a"], and we search for "a", it should still only count the item once.
		todo1 := &Todo{
			ID: 1, Title: "Test", Done: false, Priority: Low, CreatedAt: time.Now(),
			Tags: []string{"test", "test"},
		}
		tl.todos = append(tl.todos, todo1)
		tl.nextID = 2

		results := tl.ByTag("test")
		if len(results) != 1 {
			t.Errorf("Expected 1 result even with duplicate tags in the item, got %d", len(results))
		}
	})

	// --- Test Case 8: Mixed state (Done/Pending) ---
	t.Run("MixedState", func(t *testing.T) {
		tl = NewTodoList()
		// Item 1: Pending, Tag A
		todo1 := addTodo("Task A", []string{"work"})
		// Item 2: Completed, Tag A
		todo2 := addTodo("Task B", []string{"work"})
		tl.Complete(todo2.ID)
		// Item 3: Pending, Tag B
		todo3 := addTodo("Task C", []string{"personal"})

		// ByTag should return ALL items matching the tag, regardless of Done status.
		results := tl.ByTag("work")
		if len(results) != 2 {
			t.Errorf("Expected 2 results for tag 'work' (both pending and done), got %d", len(results))
		}
	})
}

func TestSearch(t *testing.T) {
	// Setup a fresh todo list
	tl := NewTodoList()

	// Create some test items
	t1 := tl.Add("Buy Milk", Low, []string{"groceries"})
	t2 := tl.Add("Finish Report", High, []string{"work"})
	t3 := tl.Add("Call Mom", Medium, []string{"personal"})
	t4 := tl.Add("Buy Bread", Low, []string{"groceries"})
	t5 := tl.Add("Review Code", High, []string{"work"})

	// --- Test Case 1: Normal usage (Title match) ---
	t.Run("Normal_TitleMatch", func(t *testing.T) {
		query := "Milk"
		results := tl.Search(query)

		if len(results) != 1 {
			t.Fatalf("Expected 1 result for '%s', got %d", query, len(results))
		}
		if results[0].ID != t1.ID {
			t.Errorf("Expected result ID %d, got %d", t1.ID, results[0].ID)
		}
	})

	// --- Test Case 2: Normal usage (Partial match) ---
	t.Run("Normal_PartialMatch", func(t *testing.T) {
		query := "report" // Case insensitive check (strings.Contains is case sensitive, but we test what it does)
		results := tl.Search(query)

		if len(results) != 1 {
			t.Fatalf("Expected 1 result for '%s', got %d", query, len(results))
		}
		if results[0].ID != t2.ID {
			t.Errorf("Expected result ID %d, got %d", t2.ID, results[0].ID)
		}
	})

	// --- Test Case 3: Multiple matches ---
	t.Run("MultipleMatches", func(t *testing.T) {
		query := "Buy"
		results := tl.Search(query)

		if len(results) != 2 {
			t.Fatalf("Expected 2 results for '%s', got %d", query, len(results))
		}
		// Check if both t1 and t4 are present (order might vary depending on internal slice order, but we check count)
		foundIDs := make(map[int]bool)
		for _, r := range results {
			foundIDs[r.ID] = true
		}
		if !foundIDs[t1.ID] || !foundIDs[t4.ID] {
			t.Errorf("Expected results to include IDs %d and %d, found IDs: %v", t1.ID, t4.ID, foundIDs)
		}
	})

	// --- Test Case 4: Edge Case - Empty query ---
	t.Run("EdgeCase_EmptyQuery", func(t *testing.T) {
		query := ""
		results := tl.Search(query)

		// strings.Contains("", "any string") is always true.
		// Therefore, it should return all items.
		if len(results) != 5 {
			t.Fatalf("Expected 5 results for empty query, got %d", len(results))
		}
	})

	// --- Test Case 5: Edge Case - No matches ---
	t.Run("EdgeCase_NoMatches", func(t *testing.T) {
		query := "NonExistentQueryXYZ"
		results := tl.Search(query)

		if len(results) != 0 {
			t.Errorf("Expected 0 results for '%s', got %d", query, len(results))
		}
	})

	// --- Test Case 6: Case Sensitivity ---
	t.Run("CaseSensitivity", func(t *testing.T) {
		// t2 title is "Finish Report"
		query := "report" // Lowercase 'r'
		results := tl.Search(query)

		if len(results) != 0 {
			t.Errorf("Expected 0 results for lowercase '%s' (case sensitive), got %d", query, len(results))
		}

		// Check for exact case match
		query = "Report"
		results = tl.Search(query)
		if len(results) != 1 || results[0].ID != t2.ID {
			t.Errorf("Expected 1 result for exact case '%s', got %d", query, len(results))
		}
	})

	// --- Test Case 7: State change (Completed items should still be searchable) ---
	t.Run("SearchAfterCompletion", func(t *testing.T) {
		// Complete t3
		_ = tl.Complete(t3.ID)

		// Search for something that only exists in a completed item
		query := "Mom"
		results := tl.Search(query)

		if len(results) != 1 {
			t.Fatalf("Expected 1 result for '%s' even if completed, got %d", query, len(results))
		}
		if results[0].ID != t3.ID {
			t.Errorf("Expected result ID %d, got %d", t3.ID, results[0].ID)
		}
	})

	// --- Test Case 8: Zero/Empty Inputs (Testing robustness) ---
	t.Run("EdgeCase_ZeroInputs", func(t *testing.T) {
		// Re-initialize list to ensure clean state for zero tests
		tlZero := NewTodoList()

		// Search with empty query (already covered, but good to re-verify)
		results := tlZero.Search("")
		if len(results) != 0 {
			t.Errorf("Expected 0 results on empty list search, got %d", len(results))
		}

		// Search with a query that is too long/complex (should just return 0)
		results = tlZero.Search("A_VERY_LONG_NON_EXISTENT_QUERY_STRING")
		if len(results) != 0 {
			t.Errorf("Expected 0 results for long query on empty list, got %d", len(results))
		}
	})
}

func TestStats(t *testing.T) {
	// Helper function to create a list with specific initial state
	setupList := func(todos ...*Todo) *TodoList {
		tl := &TodoList{
			todos:  todos,
			nextID: 100, // Ensure IDs don't conflict with manual setup
		}
		return tl
	}

	t.Run("NormalUsage_MixedStatus", func(t *testing.T) {
		// Setup: 1 Done, 2 Pending
		todo1 := &Todo{ID: 1, Done: true}
		todo2 := &Todo{ID: 2, Done: false}
		todo3 := &Todo{ID: 3, Done: false}
		list := setupList(todo1, todo2, todo3)

		total, done, pending := list.Stats()

		if total != 3 {
			t.Errorf("Expected total 3, got %d", total)
		}
		if done != 1 {
			t.Errorf("Expected done 1, got %d", done)
		}
		if pending != 2 {
			t.Errorf("Expected pending 2, got %d", pending)
		}
	})

	t.Run("EdgeCase_AllPending", func(t *testing.T) {
		// Setup: 3 Pending
		todo1 := &Todo{ID: 1, Done: false}
		todo2 := &Todo{ID: 2, Done: false}
		todo3 := &Todo{ID: 3, Done: false}
		list := setupList(todo1, todo2, todo3)

		total, done, pending := list.Stats()

		if total != 3 {
			t.Errorf("Expected total 3, got %d", total)
		}
		if done != 0 {
			t.Errorf("Expected done 0, got %d", done)
		}
		if pending != 3 {
			t.Errorf("Expected pending 3, got %d", pending)
		}
	})

	t.Run("EdgeCase_AllCompleted", func(t *testing.T) {
		// Setup: 3 Done
		todo1 := &Todo{ID: 1, Done: true}
		todo2 := &Todo{ID: 2, Done: true}
		todo3 := &Todo{ID: 3, Done: true}
		list := setupList(todo1, todo2, todo3)

		total, done, pending := list.Stats()

		if total != 3 {
			t.Errorf("Expected total 3, got %d", total)
		}
		if done != 3 {
			t.Errorf("Expected done 3, got %d", done)
		}
		if pending != 0 {
			t.Errorf("Expected pending 0, got %d", pending)
		}
	})

	t.Run("EdgeCase_EmptyList", func(t *testing.T) {
		// Setup: Empty list
		list := setupList()

		total, done, pending := list.Stats()

		if total != 0 {
			t.Errorf("Expected total 0, got %d", total)
		}
		if done != 0 {
			t.Errorf("Expected done 0, got %d", done)
		}
		if pending != 0 {
			t.Errorf("Expected pending 0, got %d", pending)
		}
	})

	t.Run("EdgeCase_SingleItem", func(t *testing.T) {
		// Test single pending item
		listPending := setupList(&Todo{ID: 1, Done: false})
		total, done, pending := listPending.Stats()
		if total != 1 || done != 0 || pending != 1 {
			t.Errorf("Pending single item failed: Total=%d, Done=%d, Pending=%d", total, done, pending)
		}

		// Test single completed item
		listDone := setupList(&Todo{ID: 2, Done: true})
		total, done, pending = listDone.Stats()
		if total != 1 || done != 1 || pending != 0 {
			t.Errorf("Done single item failed: Total=%d, Done=%d, Pending=%d", total, done, pending)
		}
	})

	t.Run("DuplicateInputs_ShouldNotAffectStats", func(t *testing.T) {
		// Note: Since the internal structure is a slice of pointers, adding the same *Todo pointer multiple times
		// would technically count it multiple times if we were testing Add, but here we are testing Stats on a pre-built list.
		// We simulate duplicate *objects* being present in the list structure.
		todo1 := &Todo{ID: 1, Done: false}
		todo2 := &Todo{ID: 2, Done: true}
		// Simulate a duplicate entry (though IDs should ideally prevent this in a real scenario)
		todo1_duplicate := &Todo{ID: 1, Done: false}

		list := setupList(todo1, todo2, todo1_duplicate)

		total, done, pending := list.Stats()

		// The implementation iterates over the slice length, so it counts 3 items.
		if total != 3 {
			t.Errorf("Expected total 3, got %d", total)
		}
		if done != 1 {
			t.Errorf("Expected done 1, got %d", done)
		}
		if pending != 2 {
			t.Errorf("Expected pending 2, got %d", pending)
		}
	})
}

func TestSorted(t *testing.T) {
	// Helper function to create a todo list with specific items
	setupList := func() *TodoList {
		tl := NewTodoList()
		// Manually set up todos to control state for testing
		todos := []*Todo{
			{ID: 1, Title: "Low Prio Task", Done: false, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour), Tags: []string{"a"}},
			{ID: 2, Title: "High Prio Task", Done: false, Priority: High, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour), Tags: []string{"b"}},
			{ID: 3, Title: "Medium Prio Task", Done: true, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour), Tags: []string{"c"}},
			{ID: 4, Title: "Another Low Prio", Done: false, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour), Tags: []string{"d"}},
			{ID: 5, Title: "High Prio Done", Done: true, Priority: High, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour), Tags: []string{"e"}},
		}
		// Overwrite the internal state of the list for predictable testing
		tl.todos = todos
		tl.nextID = 6
		return tl
	}

	t.Run("NormalUsage_MixedPriorities", func(t *testing.T) {
		tl := setupList()
		sorted := tl.Sorted()

		if len(sorted) != 5 {
			t.Fatalf("Expected 5 sorted items, got %d", len(sorted))
		}

		// Expected order: High (2), High (5), Medium (3), Low (1), Low (4)
		// Note: The sort is stable for equal priorities, but we only check the priority order.
		expectedOrderIDs := []int{2, 5, 3, 1, 4}
		for i, expectedID := range expectedOrderIDs {
			if sorted[i].ID != expectedID {
				t.Errorf("Index %d: Expected ID %d, got ID %d", i, expectedID, sorted[i].ID)
			}
		}
	})

	t.Run("EdgeCase_AllSamePriority", func(t *testing.T) {
		tl := NewTodoList()
		// Manually set up all items with Medium priority
		todos := []*Todo{
			{ID: 1, Title: "A", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 2, Title: "B", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 3, Title: "C", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 4

		sorted := tl.Sorted()
		if len(sorted) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(sorted))
		}
		// Since all priorities are equal, the order should be preserved relative to the input (or arbitrary, but consistent)
		// We just check that all items are present.
		ids := make(map[int]bool)
		for _, t := range sorted {
			ids[t.ID] = true
		}
		if !ids[1] || !ids[2] || !ids[3] {
			t.Errorf("Did not contain all expected IDs: 1, 2, 3")
		}
	})

	t.Run("EdgeCase_EmptyList", func(t *testing.T) {
		tl := NewTodoList()
		sorted := tl.Sorted()
		if len(sorted) != 0 {
			t.Errorf("Expected empty slice for empty list, got length %d", len(sorted))
		}
	})

	t.Run("EdgeCase_AllCompleted", func(t *testing.T) {
		tl := NewTodoList()
		// Manually set up all items as done
		todos := []*Todo{
			{ID: 1, Title: "Done 1", Done: true, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 2, Title: "Done 2", Done: true, Priority: High, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 3

		sorted := tl.Sorted()
		if len(sorted) != 2 {
			t.Fatalf("Expected 2 items, got %d", len(sorted))
		}
		// The sort function does not filter by Done status, so all items should be returned.
		if sorted[0].ID != 2 || sorted[1].ID != 1 { // High (2) > Low (1)
			t.Errorf("Expected order based on priority, got IDs %d, %d", sorted[0].ID, sorted[1].ID)
		}
	})

	t.Run("EdgeCase_AllPending", func(t *testing.T) {
		tl := NewTodoList()
		// Manually set up all items as pending
		todos := []*Todo{
			{ID: 1, Title: "Pending 1", Done: false, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 2, Title: "Pending 2", Done: false, Priority: High, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 3

		sorted := tl.Sorted()
		if len(sorted) != 2 {
			t.Fatalf("Expected 2 items, got %d", len(sorted))
		}
		// Expected order: High (2), Low (1)
		if sorted[0].ID != 2 || sorted[1].ID != 1 {
			t.Errorf("Expected order based on priority, got IDs %d, %d", sorted[0].ID, sorted[1].ID)
		}
	})

	t.Run("DuplicateInputs_ShouldHandleAll", func(t *testing.T) {
		// Since Sorted operates on the internal slice, duplicate inputs are just multiple entries.
		// We test that the sorting logic handles multiple items of the same priority correctly.
		tl := NewTodoList()
		todos := []*Todo{
			{ID: 1, Title: "A", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 2, Title: "B", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 3, Title: "C", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 4

		sorted := tl.Sorted()
		if len(sorted) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(sorted))
		}
		// All Medium, order should be stable/consistent
		ids := map[int]bool{1: true, 2: true, 3: true}
		for _, t := range sorted {
			if !ids[t.ID] {
				t.Errorf("Unexpected ID found in sorted list: %d", t.ID)
			}
		}
	})

	t.Run("CaseSensitivity_TitleDoesNotAffectSort", func(t *testing.T) {
		// Title content should not affect the priority sort order.
		tl := NewTodoList()
		todos := []*Todo{
			{ID: 1, Title: "apple", Done: false, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 2, Title: "APPLE", Done: false, Priority: High, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
			{ID: 3, Title: "banana", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 4

		sorted := tl.Sorted()
		if len(sorted) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(sorted))
		}
		// Expected order: High (2), Medium (3), Low (1)
		if sorted[0].ID != 2 || sorted[1].ID != 3 || sorted[2].ID != 1 {
			t.Errorf("Expected order based on priority, got IDs %d, %d, %d", sorted[0].ID, sorted[1].ID, sorted[2].ID)
		}
	})

	t.Run("TimeEdgeCases_DueDateIsIgnored", func(t *testing.T) {
		// The Sorted method only uses Priority, so DueDate/CreatedAt should not affect the order.
		tl := NewTodoList()
		todos := []*Todo{
			// Low priority, but due far in the future
			{ID: 1, Title: "Low Future", Done: false, Priority: Low, CreatedAt: time.Now(), DueDate: time.Now().Add(time.Hour * 24 * 365), Tags: nil},
			// High priority, but due in the past
			{ID: 2, Title: "High Past", Done: false, Priority: High, CreatedAt: time.Now(), DueDate: time.Now().Add(-time.Hour), Tags: nil},
			// Medium priority, due now
			{ID: 3, Title: "Medium Now", Done: false, Priority: Medium, CreatedAt: time.Now(), DueDate: time.Now(), Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 4

		sorted := tl.Sorted()
		if len(sorted) != 3 {
			t.Fatalf("Expected 3 items, got %d", len(sorted))
		}
		// Expected order: High (2), Medium (3), Low (1)
		if sorted[0].ID != 2 || sorted[1].ID != 3 || sorted[2].ID != 1 {
			t.Errorf("Expected order based on priority, got IDs %d, %d, %d", sorted[0].ID, sorted[1].ID, sorted[2].ID)
		}
	})

	t.Run("TimeEdgeCases_ZeroTime", func(t *testing.T) {
		// Zero time should be treated like any other time for comparison purposes,
		// but since the sort only uses Priority, this should still work.
		tl := NewTodoList()
		todos := []*Todo{
			// Low priority, zero time
			{ID: 1, Title: "Low Zero", Done: false, Priority: Low, CreatedAt: time.Time{}, DueDate: time.Time{}, Tags: nil},
			// High priority, zero time
			{ID: 2, Title: "High Zero", Done: false, Priority: High, CreatedAt: time.Time{}, DueDate: time.Time{}, Tags: nil},
		}
		tl.todos = todos
		tl.nextID = 3

		sorted := tl.Sorted()
		if len(sorted) != 2 {
			t.Fatalf("Expected 2 items, got %d", len(sorted))
		}
		// Expected order: High (2), Low (1)
		if sorted[0].ID != 2 || sorted[1].ID != 1 {
			t.Errorf("Expected order based on priority, got IDs %d, %d", sorted[0].ID, sorted[1].ID)
		}
	})
}

func TestAddAndRemoveTag(t *testing.T) {
	// Setup: Create a fresh todo list
	tl := NewTodoList()

	// Helper to create a todo item with specific tags
	addTodo := func(title string, tags []string) *Todo {
		// We need to mock time.Now() or accept that the ID will be sequential.
		// For testing AddTag/RemoveTag, we just need a valid ID.
		// We'll use the existing Add method, accepting the time dependency.
		return tl.Add(title, Medium, tags)
	}

	// --- Test Case 1: Normal Usage (Add and Remove) ---
	t.Run("NormalAddAndRemove", func(t *testing.T) {
		todo := addTodo("Buy groceries", []string{"errand", "food"})
		id := todo.ID

		// 1. Add a new tag
		err := tl.AddTag(id, "urgent")
		if err != nil {
			t.Fatalf("Expected no error when adding tag, got: %v", err)
		}

		// Verify tag addition
		retrievedTodo, err := tl.Get(id)
		if err != nil {
			t.Fatalf("Failed to get todo: %v", err)
		}
		expectedTags := []string{"errand", "food", "urgent"}
		if !compareStringSlices(retrievedTodo.Tags, expectedTags) {
			t.Errorf("Tags mismatch. Expected %v, got %v", expectedTags, retrievedTodo.Tags)
		}

		// 2. Remove an existing tag
		err = tl.RemoveTag(id, "errand")
		if err != nil {
			t.Fatalf("Expected no error when removing tag, got: %v", err)
		}

		// Verify tag removal
		retrievedTodo, _ = tl.Get(id)
		expectedTagsAfterRemoval := []string{"food", "urgent"}
		if !compareStringSlices(retrievedTodo.Tags, expectedTagsAfterRemoval) {
			t.Errorf("Tags mismatch after removal. Expected %v, got %v", expectedTagsAfterRemoval, retrievedTodo.Tags)
		}
	})

	// --- Test Case 2: Edge Case - Zero/Empty Inputs ---
	t.Run("EdgeCaseEmptyInputs", func(t *testing.T) {
		// Setup a todo with no initial tags
		todo := addTodo("Empty test", []string{})
		id := todo.ID

		// 1. Add tag with empty string (should treat it as a valid tag)
		err := tl.AddTag(id, "")
		if err != nil {
			t.Fatalf("Expected no error when adding empty tag, got: %v", err)
		}
		retrievedTodo, _ := tl.Get(id)
		if len(retrievedTodo.Tags) != 1 || retrievedTodo.Tags[0] != "" {
			t.Errorf("Expected tags to contain [''], got %v", retrievedTodo.Tags)
		}

		// 2. Attempt to remove a non-existent tag
		err = tl.RemoveTag(id, "nonexistent")
		if err == nil {
			t.Fatal("Expected error when removing non-existent tag, got nil")
		}
		expectedErrMsg := "tag nonexistent not found"
		if err.Error() != expectedErrMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
		}

		// 3. Attempt to add tag to non-existent ID
		err = tl.AddTag(999, "test")
		if err == nil {
			t.Fatal("Expected error when adding tag to non-existent ID, got nil")
		}
		expectedErrMsg = "todo 999 not found"
		if err.Error() != expectedErrMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
		}

		// 4. Attempt to remove tag from non-existent ID
		err = tl.RemoveTag(999, "test")
		if err == nil {
			t.Fatal("Expected error when removing tag from non-existent ID, got nil")
		}
		expectedErrMsg = "todo 999 not found"
		if err.Error() != expectedErrMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
		}
	})

	// --- Test Case 3: Duplicate Inputs and Case Sensitivity ---
	t.Run("DuplicateAndCaseSensitivity", func(t *testing.T) {
		// Setup a todo with initial tags
		todo := addTodo("Case test", []string{"TagA", "tagb"})
		id := todo.ID

		// 1. Add a duplicate tag (should append it)
		err := tl.AddTag(id, "TagA")
		if err != nil {
			t.Fatalf("Expected no error when adding duplicate tag, got: %v", err)
		}
		retrievedTodo, _ := tl.Get(id)
		// The implementation appends, so duplicates are allowed by the current logic.
		expectedTags := []string{"TagA", "tagb", "TagA"}
		if !compareStringSlices(retrievedTodo.Tags, expectedTags) {
			t.Errorf("Tags mismatch after adding duplicate. Expected %v, got %v", expectedTags, retrievedTodo.Tags)
		}

		// 2. Remove a tag using different casing (should fail if case sensitive)
		err = tl.RemoveTag(id, "taga") // Lowercase vs initial "TagA"
		if err == nil {
			t.Fatal("Expected error when removing tag with wrong case, got nil")
		}
		expectedErrMsg := "tag taga not found"
		if err.Error() != expectedErrMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
		}

		// 3. Remove the tag correctly
		err = tl.RemoveTag(id, "tagb")
		if err != nil {
			t.Fatalf("Failed to remove existing tag: %v", err)
		}
		retrievedTodo, _ = tl.Get(id)
		expectedTagsAfterRemoval := []string{"TagA", "TagA"}
		if !compareStringSlices(retrievedTodo.Tags, expectedTagsAfterRemoval) {
			t.Errorf("Tags mismatch after removal. Expected %v, got %v", expectedTagsAfterRemoval, retrievedTodo.Tags)
		}
	})

	// --- Test Case 4: Time Edge Cases (Not directly applicable to AddTag/RemoveTag, but good practice) ---
	// Since AddTag/RemoveTag only deal with IDs and strings, time edge cases are implicitly covered
	// by the setup/teardown of the Todo object, but we ensure the ID mechanism works.
	t.Run("MultipleItemsAndIDIsolation", func(t *testing.T) {
		// Setup three items
		todo1 := addTodo("Item 1", []string{"a"})
		todo2 := addTodo("Item 2", []string{"b"})
		todo3 := addTodo("Item 3", []string{"c"})

		// Modify tags on item 2
		err := tl.AddTag(todo2.ID, "new")
		if err != nil {
			t.Fatalf("Failed to add tag to item 2: %v", err)
		}

		// Verify item 1 is untouched
		retrieved1, _ := tl.Get(todo1.ID)
		if len(retrieved1.Tags) != 1 || retrieved1.Tags[0] != "a" {
			t.Errorf("Item 1 tags were unexpectedly modified. Got %v", retrieved1.Tags)
		}

		// Modify tags on item 3
		err = tl.RemoveTag(todo3.ID, "c")
		if err != nil {
			t.Fatalf("Failed to remove tag from item 3: %v", err)
		}
		retrieved3, _ := tl.Get(todo3.ID)
		if len(retrieved3.Tags) != 0 {
			t.Errorf("Item 3 tags were not cleared correctly. Got %v", retrieved3.Tags)
		}
	})
}

// Helper function to compare string slices regardless of order (though AddTag/RemoveTag implies order)
// For this specific test, we rely on the append/slice logic which maintains order.
func compareStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSetPriorityAndRename(t *testing.T) {
	// Setup a fresh todo list
	tl := NewTodoList()

	// Create initial items with known IDs
	todo1 := tl.Add("Task A", Low, []string{"work"})
	todo2 := tl.Add("Task B", Medium, []string{"personal"})
	todo3 := tl.Add("Task C", High, []string{"work"})

	// --- Test SetPriority ---

	t.Run("SetPriority_NormalUsage", func(t *testing.T) {
		// Change Task A's priority from Low to High
		err := tl.SetPriority(todo1.ID, High)
		if err != nil {
			t.Fatalf("Expected no error setting priority, got %v", err)
		}

		// Verify the change
		updatedTodo1, err := tl.Get(todo1.ID)
		if err != nil {
			t.Fatalf("Failed to get todo %d: %v", todo1.ID, err)
		}
		if updatedTodo1.Priority != High {
			t.Errorf("Expected priority to be High, got %v", updatedTodo1.Priority)
		}
	})

	t.Run("SetPriority_EdgeCase_NonExistentID", func(t *testing.T) {
		// Try setting priority on an ID that doesn't exist
		err := tl.SetPriority(999, Medium)
		if err == nil {
			t.Fatal("Expected an error for non-existent ID, got nil")
		}
		expectedError := "todo 999 not found"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("SetPriority_EdgeCase_ZeroValueID", func(t *testing.T) {
		// Try setting priority on ID 0 (assuming IDs start at 1)
		err := tl.SetPriority(0, Medium)
		if err == nil {
			t.Fatal("Expected an error for ID 0, got nil")
		}
	})

	t.Run("SetPriority_EdgeCase_AlreadySetPriority", func(t *testing.T) {
		// Set Task B's priority to Medium, then set it back to Medium (no change)
		err := tl.SetPriority(todo2.ID, Medium)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		// Just checking that it doesn't panic or error out on redundant calls
	})

	// --- Test Rename ---

	t.Run("Rename_NormalUsage", func(t *testing.T) {
		newTitle := "Updated Task A Title"
		err := tl.Rename(todo1.ID, newTitle)
		if err != nil {
			t.Fatalf("Expected no error renaming, got %v", err)
		}

		// Verify the change
		updatedTodo1, err := tl.Get(todo1.ID)
		if err != nil {
			t.Fatalf("Failed to get todo %d: %v", todo1.ID, err)
		}
		if updatedTodo1.Title != newTitle {
			t.Errorf("Expected title to be '%s', got '%s'", newTitle, updatedTodo1.Title)
		}
	})

	t.Run("Rename_EdgeCase_EmptyTitle", func(t *testing.T) {
		// Rename Task B to an empty string
		err := tl.Rename(todo2.ID, "")
		if err != nil {
			t.Fatalf("Expected no error renaming to empty string, got %v", err)
		}

		// Verify the change
		updatedTodo2, err := tl.Get(todo2.ID)
		if err != nil {
			t.Fatalf("Failed to get todo %d: %v", todo2.ID, err)
		}
		if updatedTodo2.Title != "" {
			t.Errorf("Expected title to be empty, got '%s'", updatedTodo2.Title)
		}
	})

	t.Run("Rename_EdgeCase_NonExistentID", func(t *testing.T) {
		// Try renaming a non-existent ID
		err := tl.Rename(999, "Ghost Task")
		if err == nil {
			t.Fatal("Expected an error for non-existent ID, got nil")
		}
		expectedError := "todo 999 not found"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("Rename_EdgeCase_DuplicateInputs", func(t *testing.T) {
		// Renaming the same task multiple times should succeed without error
		err := tl.Rename(todo3.ID, "New Title 1")
		if err != nil {
			t.Fatalf("Unexpected error on first rename: %v", err)
		}
		err = tl.Rename(todo3.ID, "New Title 2")
		if err != nil {
			t.Fatalf("Unexpected error on second rename: %v", err)
		}

		// Verify final state
		updatedTodo3, _ := tl.Get(todo3.ID)
		if updatedTodo3.Title != "New Title 2" {
			t.Errorf("Expected final title to be 'New Title 2', got '%s'", updatedTodo3.Title)
		}
	})

	t.Run("Rename_CaseSensitivity", func(t *testing.T) {
		// Renaming should handle case changes correctly
		newTitle := "Mixed Case Title"
		err := tl.Rename(todo2.ID, newTitle)
		if err != nil {
			t.Fatalf("Unexpected error on case change: %v", err)
		}

		updatedTodo2, _ := tl.Get(todo2.ID)
		if updatedTodo2.Title != newTitle {
			t.Errorf("Expected title to be case-sensitive '%s', got '%s'", newTitle, updatedTodo2.Title)
		}
	})
}

func TestPendingAndCompletedAndClearCompleted(t *testing.T) {
	// Helper function to create a list with specific states
	setupList := func() *TodoList {
		tl := NewTodoList()
		// Item 1: Pending, Low Priority
		t1 := &Todo{ID: 1, Title: "Pending Low", Done: false, Priority: Low, CreatedAt: time.Now().Add(-2 * time.Hour)}
		// Item 2: Completed, High Priority
		t2 := &Todo{ID: 2, Title: "Completed High", Done: true, Priority: High, CreatedAt: time.Now().Add(-1 * time.Hour)}
		// Item 3: Pending, High Priority
		t3 := &Todo{ID: 3, Title: "Pending High", Done: false, Priority: High, CreatedAt: time.Now()}
		// Item 4: Completed, Medium Priority
		t4 := &Todo{ID: 4, Title: "Completed Medium", Done: true, Priority: Medium, CreatedAt: time.Now().Add(-3 * time.Hour)}
		// Item 5: Pending, Low Priority (Edge case: zero time for creation)
		t5 := &Todo{ID: 5, Title: "Pending ZeroTime", Done: false, Priority: Low, CreatedAt: time.Time{}}
		// Item 6: Completed, Medium Priority (Edge case: zero time for creation)
		t6 := &Todo{ID: 6, Title: "Completed ZeroTime", Done: true, Priority: Medium, CreatedAt: time.Time{}}

		// Manually set the internal state for testing consistency, bypassing Add() which uses time.Now()
		tl.todos = []*Todo{t1, t2, t3, t4, t5, t6}
		tl.nextID = 7 // Set nextID high enough to avoid conflict
		return tl
	}

	t.Run("NormalUsage", func(t *testing.T) {
		tl := setupList()

		// Test Pending()
		pending := tl.Pending()
		if len(pending) != 3 {
			t.Errorf("Expected 3 pending items, got %d", len(pending))
		}
		pendingTitles := map[string]bool{"Pending Low": true, "Pending High": true, "Pending ZeroTime": true}
		for _, t := range pending {
			if !pendingTitles[t.Title] {
				t.Errorf("Unexpected pending item found: %s", t.Title)
			}
		}

		// Test Completed()
		completed := tl.Completed()
		if len(completed) != 3 {
			t.Errorf("Expected 3 completed items, got %d", len(completed))
		}
		completedTitles := map[string]bool{"Completed High": true, "Completed Medium": true, "Completed ZeroTime": true}
		for _, t := range completed {
			if !completedTitles[t.Title] {
				t.Errorf("Unexpected completed item found: %s", t.Title)
			}
		}

		// Test ClearCompleted()
		initialCount := len(tl.todos)
		clearedCount := tl.ClearCompleted()
		if clearedCount != 3 {
			t.Errorf("Expected 3 items to be cleared, got %d", clearedCount)
		}
		if len(tl.todos) != 3 {
			t.Errorf("Expected 3 remaining items, got %d", len(tl.todos))
		}
		// Verify that the remaining items are the pending ones
		remainingTitles := make(map[string]bool)
		for _, t := range tl.todos {
			remainingTitles[t.Title] = true
		}
		if !remainingTitles["Pending Low"] || !remainingTitles["Pending High"] || !remainingTitles["Pending ZeroTime"] {
			t.Errorf("Remaining list does not contain all expected pending items.")
		}
	})

	t.Run("EdgeCases", func(t *testing.T) {
		// Case 1: Empty List
		tlEmpty := NewTodoList()
		if len(tlEmpty.Pending()) != 0 {
			t.Error("Expected 0 pending items in an empty list")
		}
		if len(tlEmpty.Completed()) != 0 {
			t.Error("Expected 0 completed items in an empty list")
		}
		cleared := tlEmpty.ClearCompleted()
		if cleared != 0 {
			t.Errorf("Expected 0 cleared items, got %d", cleared)
		}
		if len(tlEmpty.todos) != 0 {
			t.Errorf("Expected 0 items remaining, got %d", len(tlEmpty.todos))
		}

		// Case 2: All Pending
		tlAllPending := NewTodoList()
		t1 := &Todo{ID: 1, Title: "P1", Done: false, Priority: Low, CreatedAt: time.Now()}
		t2 := &Todo{ID: 2, Title: "P2", Done: false, Priority: High, CreatedAt: time.Now()}
		tlAllPending.todos = []*Todo{t1, t2}
		
		pending := tlAllPending.Pending()
		if len(pending) != 2 {
			t.Errorf("Expected 2 pending items, got %d", len(pending))
		}
		completed := tlAllPending.Completed()
		if len(completed) != 0 {
			t.Errorf("Expected 0 completed items, got %d", len(completed))
		}
		
		cleared = tlAllPending.ClearCompleted()
		if cleared != 0 {
			t.Errorf("Expected 0 cleared items, got %d", cleared)
		}
		if len(tlAllPending.todos) != 2 {
			t.Errorf("Expected 2 items remaining, got %d", len(tlAllPending.todos))
		}

		// Case 3: All Completed
		tlAllCompleted := NewTodoList()
		t1 = &Todo{ID: 1, Title: "C1", Done: true, Priority: Low, CreatedAt: time.Now()}
		t2 = &Todo{ID: 2, Title: "C2", Done: true, Priority: High, CreatedAt: time.Now()}
		tlAllCompleted.todos = []*Todo{t1, t2}

		pending = tlAllCompleted.Pending()
		if len(pending) != 0 {
			t.Errorf("Expected 0 pending items, got %d", len(pending))
		}
		completed = tlAllCompleted.Completed()
		if len(completed) != 2 {
			t.Errorf("Expected 2 completed items, got %d", len(completed))
		}
		
		cleared = tlAllCompleted.ClearCompleted()
		if cleared != 2 {
			t.Errorf("Expected 2 cleared items, got %d", cleared)
		}
		if len(tlAllCompleted.todos) != 0 {
			t.Errorf("Expected 0 items remaining, got %d", len(tlAllCompleted.todos))
		}
		
		// Case 4: Mixed list, clear all
		tlMixed := setupList() // 3 Pending, 3 Completed
		cleared = tlMixed.ClearCompleted()
		if cleared != 3 {
			t.Errorf("Expected 3 cleared items, got %d", cleared)
		}
		if len(tlMixed.todos) != 3 {
			t.Errorf("Expected 3 remaining items, got %d", len(tlMixed.todos))
		}
	})

	t.Run("DuplicateInputsAndCaseSensitivity", func(t *testing.T) {
		// Note: Pending/Completed/ClearCompleted operate on the internal slice, 
		// so duplicate *objects* are fine, but we test the logic flow.

		// Test case sensitivity (though these methods don't use strings, we ensure the logic holds)
		// We rely on the setupList having unique IDs, which is the primary key.

		// Test duplicate IDs (Shouldn't happen if Add() is used correctly, but testing robustness)
		tl := NewTodoList()
		// Manually inject two items with the same ID (This simulates a bad state)
		t1 := &Todo{ID: 1, Title: "P1", Done: false, Priority: Low, CreatedAt: time.Now()}
		t2 := &Todo{ID: 1, Title: "P1_Duplicate", Done: false, Priority: Low, CreatedAt: time.Now()}
		tl.todos = []*Todo{t1, t2}

		// Pending should find both instances if they exist in the slice
		pending := tl.Pending()
		if len(pending) != 2 {
			t.Errorf("Expected 2 pending items when duplicates exist, got %d", len(pending))
		}
		
		// ClearCompleted should process all items regardless of ID duplication
		cleared = tl.ClearCompleted()
		if cleared != 0 {
			t.Errorf("Expected 0 cleared items, got %d", cleared)
		}
	})
}

