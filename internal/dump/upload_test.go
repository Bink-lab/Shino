package dump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadTestFile(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shino-donation.zip")
	if err := os.WriteFile(path, bytesOf(size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func bytesOf(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func TestUploadDonationHappyPath(t *testing.T) {
	var parts []int
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		switch {
		case r.URL.Path == "/api/donations/uploads" && r.Method == http.MethodPost:
			var payload struct {
				FileName string `json:"fileName"`
				Size     int64  `json:"size"`
			}
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d1","uploadId":"u1","partSize":4}`)
		case strings.HasSuffix(r.URL.Path, "/part"):
			parts = append(parts, int(r.ContentLength))
			fmt.Fprint(w, `{"etag":"e"}`)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d1"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	id, err := UploadDonation(context.Background(), UploadConfig{
		BaseURL: srv.URL, APIKey: "shino_k", FilePath: uploadTestFile(t, 10),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "d1" {
		t.Fatalf("id = %q", id)
	}
	if gotKey != "shino_k" {
		t.Fatalf("api key header = %q", gotKey)
	}
	if len(parts) != 3 || parts[0] != 4 || parts[1] != 4 || parts[2] != 2 {
		t.Fatalf("parts = %v", parts)
	}
}

func TestUploadDonationRejectsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"Unauthorized"}`)
	}))
	defer srv.Close()

	_, err := UploadDonation(context.Background(), UploadConfig{
		BaseURL: srv.URL, APIKey: "bad", FilePath: uploadTestFile(t, 10),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestUploadDonationRetriesPart(t *testing.T) {
	attempts := map[string]int{}
	var parts []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/donations/uploads" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d1","uploadId":"u1","partSize":4}`)
		case strings.HasSuffix(r.URL.Path, "/part"):
			partNumber := r.URL.Query().Get("partNumber")
			attempts[partNumber]++
			if partNumber == "1" && attempts[partNumber] < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			parts = append(parts, int(r.ContentLength))
			fmt.Fprint(w, `{"etag":"e"}`)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":"d1"}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	id, err := UploadDonation(context.Background(), UploadConfig{
		BaseURL: srv.URL, APIKey: "k", FilePath: uploadTestFile(t, 10),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "d1" {
		t.Fatalf("id = %q", id)
	}
	if attempts["1"] != 3 {
		t.Fatalf("first part attempts = %d", attempts["1"])
	}
	if len(parts) != 3 || parts[0] != 4 || parts[1] != 4 || parts[2] != 2 {
		t.Fatalf("parts = %v", parts)
	}
}

func TestUploadDonationFailsOnPartError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/part") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"d1","uploadId":"u1","partSize":4}`)
	}))
	defer srv.Close()

	_, err := UploadDonation(context.Background(), UploadConfig{
		BaseURL: srv.URL, APIKey: "k", FilePath: uploadTestFile(t, 10),
	}, nil)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("expected part error, got %v", err)
	}
}
