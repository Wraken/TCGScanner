package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

const (
	driveBase   = "https://www.googleapis.com/drive/v3"
	driveUpload = "https://www.googleapis.com/upload/drive/v3"
	driveFolder = "application/vnd.google-apps.folder"
)

// driveClient is a thin Google Drive v3 REST client. HTTP is expected to
// carry the authorization (an OAuth transport).
type driveClient struct {
	HTTP         *http.Client
	Base, Upload string
}

type driveFile struct {
	ID, Name, ModifiedTime string
	Size                   int64
}

// driveError is any non-2xx answer from Drive.
type driveError struct {
	Status int
	Reason string
}

func (e *driveError) Error() string { return fmt.Sprintf("Drive: %d %s", e.Status, e.Reason) }

func (c *driveClient) base() string {
	if c.Base != "" {
		return c.Base
	}
	return driveBase
}

func (c *driveClient) upload() string {
	if c.Upload != "" {
		return c.Upload
	}
	return driveUpload
}

// do sends one request and returns the body of a 2xx answer.
func (c *driveClient) do(ctx context.Context, method, u, contentType string, body []byte) ([]byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		reason := http.StatusText(resp.StatusCode)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			reason = e.Error.Message
		}
		return nil, &driveError{Status: resp.StatusCode, Reason: reason}
	}
	return data, nil
}

// idOf decodes the {"id"} answer of a create call.
func idOf(data []byte) (string, error) {
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.ID, nil
}

// list runs a files query and follows nextPageToken.
func (c *driveClient) list(ctx context.Context, q string) ([]driveFile, error) {
	var out []driveFile
	token := ""
	for {
		v := url.Values{}
		v.Set("q", q)
		v.Set("fields", "files(id,name,modifiedTime,size),nextPageToken")
		v.Set("pageSize", "1000")
		v.Set("spaces", "drive")
		if token != "" {
			v.Set("pageToken", token)
		}
		data, err := c.do(ctx, http.MethodGet, c.base()+"/files?"+v.Encode(), "", nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Files []struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				ModifiedTime string `json:"modifiedTime"`
				Size         string `json:"size"` // a string in Drive's JSON
			} `json:"files"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, err
		}
		for _, f := range page.Files {
			size, _ := strconv.ParseInt(f.Size, 10, 64)
			out = append(out, driveFile{ID: f.ID, Name: f.Name, ModifiedTime: f.ModifiedTime, Size: size})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}

var driveQuoter = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

// findFolder returns the id of the folder called name, "" when there is none.
func (c *driveClient) findFolder(ctx context.Context, name string) (string, error) {
	files, err := c.list(ctx, "mimeType = '"+driveFolder+"' and name = '"+driveQuoter.Replace(name)+"' and trashed = false")
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0].ID, nil
}

func (c *driveClient) createFolder(ctx context.Context, name string) (string, error) {
	body, _ := json.Marshal(map[string]string{"name": name, "mimeType": driveFolder})
	data, err := c.do(ctx, http.MethodPost, c.base()+"/files", "application/json; charset=UTF-8", body)
	if err != nil {
		return "", err
	}
	return idOf(data)
}

func (c *driveClient) listJournals(ctx context.Context, folderID string) ([]driveFile, error) {
	return c.list(ctx, "'"+driveQuoter.Replace(folderID)+"' in parents and trashed = false and name contains 'journal-'")
}

func (c *driveClient) download(ctx context.Context, fileID string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, c.base()+"/files/"+url.PathEscape(fileID)+"?alt=media", "", nil)
}

func (c *driveClient) create(ctx context.Context, folderID, name string, content []byte) (string, error) {
	meta, _ := json.Marshal(map[string]any{"name": name, "parents": []string{folderID}, "mimeType": "text/plain"})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return "", err
	}
	part.Write(meta)
	part, err = mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain"}})
	if err != nil {
		return "", err
	}
	part.Write(content)
	if err := mw.Close(); err != nil {
		return "", err
	}
	data, err := c.do(ctx, http.MethodPost, c.upload()+"/files?uploadType=multipart", "multipart/related; boundary="+mw.Boundary(), buf.Bytes())
	if err != nil {
		return "", err
	}
	return idOf(data)
}

func (c *driveClient) update(ctx context.Context, fileID string, content []byte) error {
	if content == nil {
		content = []byte{}
	}
	_, err := c.do(ctx, http.MethodPatch, c.upload()+"/files/"+url.PathEscape(fileID)+"?uploadType=media", "text/plain", content)
	return err
}

// folderAlive reports whether a cached folder id still names a folder that
// is not in the trash; a 404 is false, not an error.
func (c *driveClient) folderAlive(ctx context.Context, id string) (bool, error) {
	data, err := c.do(ctx, http.MethodGet, c.base()+"/files/"+url.PathEscape(id)+"?fields=id,trashed", "", nil)
	var de *driveError
	if errors.As(err, &de) && de.Status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var r struct {
		Trashed bool `json:"trashed"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return false, err
	}
	return !r.Trashed, nil
}
