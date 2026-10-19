package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDriveClientRoundTrip(t *testing.T) {
	f := newFakeDrive(t)
	c := f.client()
	ctx := context.Background()
	if id, err := c.findFolder(ctx, syncFolderName); err != nil || id != "" {
		t.Fatal(id, err)
	}
	folder, err := c.createFolder(ctx, syncFolderName)
	if err != nil || folder == "" {
		t.Fatal(folder, err)
	}
	if id, _ := c.findFolder(ctx, syncFolderName); id != folder {
		t.Error("findFolder after create")
	}
	fileID, err := c.create(ctx, folder, "journal-pc.jsonl", []byte("line1\n"))
	if err != nil || fileID == "" {
		t.Fatal(fileID, err)
	}
	if err := c.update(ctx, fileID, []byte("line1\nline2\n")); err != nil {
		t.Fatal(err)
	}
	got, err := c.download(ctx, fileID)
	if err != nil || string(got) != "line1\nline2\n" {
		t.Fatal(string(got), err)
	}
	f.put(folder, "notes.txt", "x")
	files, err := c.listJournals(ctx, folder)
	if err != nil || len(files) != 1 || files[0].Name != "journal-pc.jsonl" || files[0].ModifiedTime == "" || files[0].Size != 12 {
		t.Fatalf("%+v %v", files, err)
	}
	var de *driveError
	if _, err := c.download(ctx, "nope"); !errors.As(err, &de) || de.Status != 404 {
		t.Errorf("404 must be a driveError: %v", err)
	}
	f.down = true
	if _, err := c.listJournals(ctx, folder); err == nil {
		t.Error("a 503 is an error")
	}
	for _, call := range f.calls {
		if strings.Contains(call, "fields=") && !strings.Contains(call, "modifiedTime") {
			t.Errorf("list must ask for modifiedTime: %s", call)
		}
	}
}

type fakeDriveFile struct {
	Name, Parent, ModifiedTime string
	Content                    []byte
}

// fakeDrive is an in-memory Drive served over httptest. Tests may read and
// set its fields only while no request is in flight.
type fakeDrive struct {
	srv     *httptest.Server
	mu      sync.Mutex
	n       int
	folders map[string]string // name -> id
	files   map[string]*fakeDriveFile
	down    bool
	calls   []string
	// beforeDownload, when set, runs before a download is served and outside
	// mu: a test blocks it to hold a download in flight.
	beforeDownload func()
}

func newFakeDrive(t *testing.T) *fakeDrive {
	f := &fakeDrive{folders: map[string]string{}, files: map[string]*fakeDriveFile{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDrive) client() *driveClient {
	return &driveClient{HTTP: f.srv.Client(), Base: f.srv.URL + "/drive/v3", Upload: f.srv.URL + "/upload/drive/v3"}
}

func (f *fakeDrive) nextID() string {
	f.n++
	return fmt.Sprintf("f%d", f.n)
}

func fakeDriveNow() string { return time.Now().UTC().Format(time.RFC3339) }

// folder returns the folder's id, creating it when absent.
func (f *fakeDrive) folder(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.folders[name]; ok {
		return id
	}
	id := f.nextID()
	f.folders[name] = id
	return id
}

// put seeds a file and returns its id.
func (f *fakeDrive) put(folder, name, content string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.files[id] = &fakeDriveFile{Name: name, Parent: folder, ModifiedTime: fakeDriveNow(), Content: []byte(content)}
	return id
}

func fakeDriveFail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": status, "message": msg}})
}

func fakeDriveJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (f *fakeDrive) handle(w http.ResponseWriter, r *http.Request) {
	if hook := f.beforeDownload; hook != nil && r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media" {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	if f.down {
		fakeDriveFail(w, http.StatusServiceUnavailable, "backend unavailable")
		return
	}
	path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/upload"), "/drive/v3")
	q := r.URL.Query()
	switch {
	case path == "/files" && r.Method == http.MethodGet:
		query := q.Get("q")
		type item struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ModifiedTime string `json:"modifiedTime"`
			Size         string `json:"size"`
		}
		out := []item{}
		if strings.Contains(query, "mimeType = 'application/vnd.google-apps.folder'") {
			for name, id := range f.folders {
				if strings.Contains(query, "name = '"+name+"'") {
					out = append(out, item{ID: id, Name: name})
				}
			}
		} else {
			for id, file := range f.files {
				if strings.Contains(query, "'"+file.Parent+"' in parents") && strings.Contains(file.Name, "journal-") {
					out = append(out, item{id, file.Name, file.ModifiedTime, strconv.Itoa(len(file.Content))})
				}
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		fakeDriveJSON(w, map[string]any{"files": out})
	case path == "/files" && r.Method == http.MethodPost && q.Get("uploadType") == "":
		var m struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m.Name == "" {
			fakeDriveFail(w, http.StatusBadRequest, "bad json")
			return
		}
		id := f.nextID()
		f.folders[m.Name] = id
		fakeDriveJSON(w, map[string]string{"id": id})
	case path == "/files" && r.Method == http.MethodPost && q.Get("uploadType") == "multipart":
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			fakeDriveFail(w, http.StatusBadRequest, "bad content type")
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		var meta struct {
			Name    string
			Parents []string
		}
		var content []byte
		for i := 0; ; i++ {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if i == 0 {
				json.Unmarshal(b, &meta)
			} else {
				content = b
			}
		}
		if len(meta.Parents) != 1 || meta.Name == "" {
			fakeDriveFail(w, http.StatusBadRequest, "bad metadata")
			return
		}
		id := f.nextID()
		f.files[id] = &fakeDriveFile{Name: meta.Name, Parent: meta.Parents[0], ModifiedTime: fakeDriveNow(), Content: content}
		fakeDriveJSON(w, map[string]string{"id": id})
	case strings.HasPrefix(path, "/files/"):
		id := strings.TrimPrefix(path, "/files/")
		file, ok := f.files[id]
		if !ok {
			fakeDriveFail(w, http.StatusNotFound, "File not found: "+id)
			return
		}
		switch {
		case r.Method == http.MethodGet && q.Get("alt") == "media":
			w.Write(file.Content)
		case r.Method == http.MethodPatch && q.Get("uploadType") == "media":
			b, _ := io.ReadAll(r.Body)
			file.Content = b
			file.ModifiedTime = fakeDriveNow()
			fakeDriveJSON(w, map[string]string{"id": id})
		default:
			fakeDriveFail(w, http.StatusBadRequest, "unsupported")
		}
	default:
		fakeDriveFail(w, http.StatusNotFound, "no such route")
	}
}
