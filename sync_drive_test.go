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
	"slices"
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
		if strings.Contains(call, "fields=files") && !strings.Contains(call, "modifiedTime") {
			t.Errorf("list must ask for modifiedTime: %s", call)
		}
	}
}

// Two devices that created the folder at once: everyone settles on the
// oldest, whatever order Drive lists them in.
func TestFindFolderPicksTheOldest(t *testing.T) {
	f := newFakeDrive(t)
	first := f.addFolder(syncFolderName).ID
	f.addFolder(syncFolderName)
	id, err := f.client().findFolder(context.Background(), syncFolderName)
	if err != nil || id != first {
		t.Errorf("findFolder = %q %v, want the oldest %q", id, err, first)
	}
}

type fakeDriveFile struct {
	Name, Parent, ModifiedTime string
	Content                    []byte
	Trashed                    bool
	Created                    int // creation order
}

type fakeDriveFolder struct {
	ID, Name string
	Created  int // creation order
}

// fakeDrive is an in-memory Drive served over httptest. Tests may read and
// set its fields only while no request is in flight.
type fakeDrive struct {
	srv     *httptest.Server
	mu      sync.Mutex
	n       int
	folders []*fakeDriveFolder
	files   map[string]*fakeDriveFile
	down    bool
	calls   []string
	// beforeDownload, when set, runs before a download is served and outside
	// mu: a test blocks it to hold a download in flight.
	beforeDownload func()
}

func newFakeDrive(t *testing.T) *fakeDrive {
	f := &fakeDrive{files: map[string]*fakeDriveFile{}}
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

// folder returns the id of the oldest folder called name, creating one when
// there is none.
func (f *fakeDrive) folder(name string) string {
	if id := f.folderID(name); id != "" {
		return id
	}
	return f.addFolder(name).ID
}

// folderID is the id of the oldest folder called name, "" when none.
func (f *fakeDrive) folderID(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var oldest *fakeDriveFolder
	for _, d := range f.folders {
		if d.Name == name && (oldest == nil || d.Created < oldest.Created) {
			oldest = d
		}
	}
	if oldest == nil {
		return ""
	}
	return oldest.ID
}

// addFolder creates a folder called name, even when one exists.
func (f *fakeDrive) addFolder(name string) *fakeDriveFolder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newFolderLocked(name)
}

func (f *fakeDrive) newFolderLocked(name string) *fakeDriveFolder {
	d := &fakeDriveFolder{ID: f.nextID(), Name: name}
	d.Created = f.n
	f.folders = append(f.folders, d)
	return d
}

// removeFolders deletes every folder called name.
func (f *fakeDrive) removeFolders(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders = slices.DeleteFunc(f.folders, func(d *fakeDriveFolder) bool { return d.Name == name })
}

// fileByName is the first file called name, nil when none.
func (f *fakeDrive) fileByName(name string) (string, *fakeDriveFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, file := range f.files {
		if file.Name == name {
			return id, file
		}
	}
	return "", nil
}

// put seeds a file and returns its id.
func (f *fakeDrive) put(folder, name, content string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID()
	f.files[id] = &fakeDriveFile{Name: name, Parent: folder, ModifiedTime: fakeDriveNow(), Content: []byte(content), Created: f.n}
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
			// Drive's order is arbitrary unless asked: newest first here, so
			// a client that does not ask for createdTime picks the wrong one.
			folders := slices.Clone(f.folders)
			sort.Slice(folders, func(i, j int) bool { return folders[i].Created > folders[j].Created })
			if q.Get("orderBy") == "createdTime" {
				slices.Reverse(folders)
			}
			for _, d := range folders {
				if strings.Contains(query, "name = '"+d.Name+"'") {
					out = append(out, item{ID: d.ID, Name: d.Name})
				}
			}
		} else {
			for id, file := range f.files {
				if file.Trashed && strings.Contains(query, "trashed = false") {
					continue
				}
				if strings.Contains(query, "'"+file.Parent+"' in parents") && strings.Contains(file.Name, "journal-") {
					out = append(out, item{id, file.Name, file.ModifiedTime, strconv.Itoa(len(file.Content))})
				}
			}
			sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		}
		fakeDriveJSON(w, map[string]any{"files": out})
	case path == "/files" && r.Method == http.MethodPost && q.Get("uploadType") == "":
		var m struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m.Name == "" {
			fakeDriveFail(w, http.StatusBadRequest, "bad json")
			return
		}
		fakeDriveJSON(w, map[string]string{"id": f.newFolderLocked(m.Name).ID})
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
		f.files[id] = &fakeDriveFile{Name: meta.Name, Parent: meta.Parents[0], ModifiedTime: fakeDriveNow(), Content: content, Created: f.n}
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
		case r.Method == http.MethodGet && q.Get("fields") == "id,trashed":
			fakeDriveJSON(w, map[string]any{"id": id, "trashed": file.Trashed})
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
