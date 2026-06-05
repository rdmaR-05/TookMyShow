package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"tookmyshow/repository"
)

type MockEventStore struct {
	IsAvailable bool
	Price       float64
	LockErr     error
	FinalizeErr error
}

func (m *MockEventStore) GetAllEvents(ctx context.Context) ([]repository.EventDisplay, error) {
	return []repository.EventDisplay{}, nil
}

func (m *MockEventStore) GetEventByID(ctx context.Context, instanceID uuid.UUID) (repository.EventDisplay, error) {
	return repository.EventDisplay{}, nil
}

func (m *MockEventStore) IsSeatAvailable(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error) {
	return m.IsAvailable, nil
}

func (m *MockEventStore) GetSeatPrice(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (float64, error) {
	return m.Price, nil
}

func (m *MockEventStore) FinalizeOrder(ctx context.Context, userEmail string, instanceID uuid.UUID, seatID uuid.UUID, price float64) error {
	return m.FinalizeErr
}

func (m *MockEventStore) LockSeat(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error) {
	return true, m.LockErr
}

// Note: Testing BookTicketHandler fully requires mocking the Redis client. 
// Since go-redis doesn't have an interface, we can use a library like miniredis, 
// but for this basic test suite, we'll verify the HTTP handler scaffolding works.

func TestAuthHandler(t *testing.T) {
	reqBody := `{"email": "test@example.com"}`
	req := httptest.NewRequest("POST", "/login", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()

	h := &EventHandler{}
	h.AuthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 OK, got %v", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if _, exists := resp["token"]; !exists {
		t.Errorf("Expected token in response, got %v", resp)
	}
}

func TestGetAllEventsHandler(t *testing.T) {
	mockRepo := &MockEventStore{}
	h := &EventHandler{Repo: mockRepo}

	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()

	h.GetAllEventsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 OK, got %v", w.Code)
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		email := r.Context().Value(userEmailKey).(string)
		if email != "test@example.com" {
			t.Errorf("Expected email test@example.com, got %v", email)
		}
	})

	wrappedHandler := RequireAuth(testHandler)

	token, _ := GenerateToken("test@example.com")

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	if !handlerCalled {
		t.Error("Expected handler to be called")
	}
}
