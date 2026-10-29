package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"listingboard/internal/board"
	"listingboard/internal/worker"
)

func TestStartMarksTheJobSuccessful(t *testing.T) {
	srv, store, dir := newTestServerWithStore(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")
	uploadFile(t, srv, cookie, job.ID, "brochure.pdf", []byte("%PDF-1.4\n"), "application/pdf")

	res := call(srv, http.MethodPost, "/api/jobs/"+job.ID+"/start", cookie, nil, "")
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	got := decodeJob(t, res)
	if got.Status != board.StatusRunning {
		t.Fatalf("status %s, want running", got.Status)
	}
	if got.RunID == "" {
		t.Fatal("runId is empty")
	}
	for _, step := range got.Checklist {
		if step.State != board.StepPending {
			t.Fatalf("step %q is %s, want pending", step.Label, step.State)
		}
	}
	if saved := getSheet(t, srv, cookie, job.ID); saved.Status != board.StatusRunning || saved.RunID != got.RunID {
		t.Fatalf("saved sheet is %s with run %q, want running with run %q", saved.Status, saved.RunID, got.RunID)
	}

	if err := worker.New(store, dir, 0).Process(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	done := getSheet(t, srv, cookie, job.ID)
	if done.Status != board.StatusSuccess || done.RunID != got.RunID {
		t.Fatalf("after the worker the sheet is %s with run %q, want success with run %q", done.Status, done.RunID, got.RunID)
	}
	for _, step := range done.Checklist {
		if step.State != board.StepDone {
			t.Fatalf("step %q is %s, want done", step.Label, step.State)
		}
	}
}

func TestUploadKeepsTheClientFilename(t *testing.T) {
	srv, dir := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")
	data := []byte("%PDF-1.4\n")
	got := uploadFile(t, srv, cookie, job.ID, "brochure.pdf", data, "application/pdf")

	if got.Filename != "brochure.pdf" {
		t.Fatalf("filename %q, want brochure.pdf", got.Filename)
	}
	path := filepath.Join(dir, accountID(t, srv, cookie), job.ID, "body")
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("brochure was not stored at %s: %v", path, err)
	}
	if !bytes.Equal(stored, data) {
		t.Fatalf("stored %q, want %q", stored, data)
	}
	if files := filesUnder(t, dir); len(files) != 1 {
		t.Fatalf("files on disk %v, want only %s", files, path)
	}
}

func TestListHidesOtherUsersJobs(t *testing.T) {
	srv, _ := newTestServer(t)
	ada := signUp(t, srv, "ada@example.com")
	grace := signUp(t, srv, "grace@example.com")
	job := createSheet(t, srv, ada, "Market loft", "10 Market Street")

	res := call(srv, http.MethodGet, "/api/jobs", grace, nil, "")
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var jobs []board.Job
	if err := json.Unmarshal(res.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	for _, item := range jobs {
		if item.ID == job.ID {
			t.Fatal("another account can see this sheet in the list")
		}
	}
}

func TestStartRequiresASession(t *testing.T) {
	srv, _ := newTestServer(t)
	res := call(srv, http.MethodPost, "/api/jobs/"+board.NewID()+"/start", "", nil, "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", res.Code)
	}
}

func TestStartAgainKeepsTheRun(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := readySheet(t, srv, cookie, "Market loft")

	first := startSheet(t, srv, cookie, job.ID, http.StatusOK)
	second := startSheet(t, srv, cookie, job.ID, http.StatusOK)
	if second.Status != board.StatusRunning || second.RunID != first.RunID {
		t.Fatalf("second start is %s with run %q, want running with run %q", second.Status, second.RunID, first.RunID)
	}
}

func TestStartWithoutABrochure(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")

	res := call(srv, http.MethodPost, "/api/jobs/"+job.ID+"/start", cookie, nil, "")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.Code)
	}
	if msg := errorMessage(t, res); msg != "Attach a brochure first" {
		t.Fatalf("error %q", msg)
	}
	if got := getSheet(t, srv, cookie, job.ID); got.Status != board.StatusDraft {
		t.Fatalf("status %s, want draft", got.Status)
	}
}

func TestStartWhileAnotherSheetRuns(t *testing.T) {
	srv, _ := newTestServer(t)
	ada := signUp(t, srv, "ada@example.com")
	grace := signUp(t, srv, "grace@example.com")
	running := readySheet(t, srv, ada, "Market loft")
	waiting := readySheet(t, srv, ada, "Harbour view")
	startSheet(t, srv, ada, running.ID, http.StatusOK)

	startSheet(t, srv, ada, waiting.ID, http.StatusConflict)
	if got := getSheet(t, srv, ada, waiting.ID); got.Status != board.StatusDraft || got.RunID != "" {
		t.Fatalf("blocked sheet is %s with run %q, want an untouched draft", got.Status, got.RunID)
	}
	startSheet(t, srv, grace, readySheet(t, srv, grace, "Mill house").ID, http.StatusOK)
}

func TestStartLeavesAFailedSheetFailed(t *testing.T) {
	srv, store, _ := newTestServerWithStore(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := readySheet(t, srv, cookie, "Market loft")
	started := startSheet(t, srv, cookie, job.ID, http.StatusOK)
	store.setStatus(job.ID, board.StatusFailed, "Brochure is missing")

	got := startSheet(t, srv, cookie, job.ID, http.StatusOK)
	if got.Status != board.StatusFailed || got.RunID != started.RunID || got.Error != "Brochure is missing" {
		t.Fatalf("restarted failed sheet is %s with run %q and error %q", got.Status, got.RunID, got.Error)
	}
}

func TestOtherAccountsSheetIsNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	ada := signUp(t, srv, "ada@example.com")
	grace := signUp(t, srv, "grace@example.com")
	job := readySheet(t, srv, ada, "Market loft")
	base := "/api/jobs/" + job.ID

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodGet, base + "/file"},
		{http.MethodPost, base + "/start"},
	} {
		if res := call(srv, tc.method, tc.path, grace, nil, ""); res.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", tc.method, tc.path, res.Code)
		}
	}
	if res := postFile(srv, grace, job.ID, "other.txt", []byte("hello"), "text/plain"); res.Code != http.StatusNotFound {
		t.Errorf("upload: status %d, want 404", res.Code)
	}
	if got := getSheet(t, srv, ada, job.ID); got.Status != board.StatusDraft || got.Filename != "brochure.pdf" {
		t.Fatalf("owner's sheet changed: %s, %q", got.Status, got.Filename)
	}
}

func TestUploadRejectsASecondBrochure(t *testing.T) {
	srv, dir := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := readySheet(t, srv, cookie, "Market loft")

	res := postFile(srv, cookie, job.ID, "other.txt", []byte("replacement"), "text/plain")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.Code)
	}
	stored, err := os.ReadFile(filepath.Join(dir, accountID(t, srv, cookie), job.ID, "body"))
	if err != nil || !bytes.HasPrefix(stored, []byte("%PDF-")) {
		t.Fatalf("first brochure was replaced: %q, %v", stored, err)
	}
}

func TestUploadDecidesTheTypeFromTheBytes(t *testing.T) {
	cases := []struct {
		name        string
		filename    string
		contentType string
		data        []byte
		want        int
	}{
		{"pdf", "brochure.pdf", "application/pdf", []byte("%PDF-1.7 \xff\x00"), http.StatusOK},
		{"pdf with a misleading name and type", "notes.txt", "image/png", []byte("%PDF-1.4"), http.StatusOK},
		{"utf-8 text", "brochure.bin", "application/octet-stream", []byte("Світла квартира, 10 Market Street"), http.StatusOK},
		{"empty file", "brochure.txt", "text/plain", nil, http.StatusBadRequest},
		{"text with a NUL byte", "brochure.txt", "text/plain", []byte("abc\x00def"), http.StatusBadRequest},
		{"invalid utf-8", "brochure.txt", "text/plain", []byte{'a', 0xff, 'b'}, http.StatusBadRequest},
		{"binary named as pdf", "brochure.pdf", "application/pdf", []byte("\x89PNG\r\n\x1a\n"), http.StatusBadRequest},
		{"pdf magic not at the start", "brochure.pdf", "application/pdf", []byte(" %PDF-\xff"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, dir := newTestServer(t)
			cookie := signUp(t, srv, "ada@example.com")
			job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")

			res := postFile(srv, cookie, job.ID, tc.filename, tc.data, tc.contentType)
			if res.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", res.Code, tc.want, res.Body.String())
			}
			if tc.want != http.StatusOK {
				if files := filesUnder(t, dir); len(files) != 0 {
					t.Fatalf("rejected brochure left files %v", files)
				}
			}
		})
	}
}

func TestUploadSizeLimit(t *testing.T) {
	srv, dir := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	tooLarge := createSheet(t, srv, cookie, "Market loft", "10 Market Street")
	fits := createSheet(t, srv, cookie, "Harbour view", "2 Quay Road")

	res := postFile(srv, cookie, tooLarge.ID, "big.txt", bytes.Repeat([]byte("a"), 1<<20+1), "text/plain")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("1 MiB + 1 byte: status %d, want 400", res.Code)
	}
	if files := filesUnder(t, dir); len(files) != 0 {
		t.Fatalf("rejected brochure left files %v", files)
	}
	if got := getSheet(t, srv, cookie, tooLarge.ID); got.Filename != "" {
		t.Fatalf("rejected brochure was recorded as %q", got.Filename)
	}
	uploadFile(t, srv, cookie, fits.ID, "exact.txt", bytes.Repeat([]byte("a"), 1<<20), "text/plain")
}

func TestUploadKeepsAPathLikeFilenameOutOfTheFilesystem(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	srv := New(newMem(), dir, "")
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")

	got := uploadFile(t, srv, cookie, job.ID, "../../escaped.txt", []byte("hello"), "text/plain")
	if got.Filename != "../../escaped.txt" {
		t.Fatalf("filename %q, want the original name", got.Filename)
	}
	want := filepath.Join(dir, accountID(t, srv, cookie), job.ID, "body")
	if files := filesUnder(t, root); len(files) != 1 || files[0] != want {
		t.Fatalf("files on disk %v, want only %s", files, want)
	}
}

func TestDownloadReturnsTheBrochure(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")
	data := []byte("%PDF-1.4\nbody")
	uploadFile(t, srv, cookie, job.ID, "brochure.pdf", data, "text/plain")

	res := call(srv, http.MethodGet, "/api/jobs/"+job.ID+"/file", cookie, nil, "")
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Equal(res.Body.Bytes(), data) {
		t.Fatalf("body %q, want %q", res.Body.Bytes(), data)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("content type %q, want application/pdf", ct)
	}
}

func TestDownloadServesTextAsPlainText(t *testing.T) {
	srv, _ := newTestServer(t)
	cookie := signUp(t, srv, "ada@example.com")
	job := createSheet(t, srv, cookie, "Market loft", "10 Market Street")
	uploadFile(t, srv, cookie, job.ID, "notes.html", []byte("<html><script>alert(1)</script></html>"), "text/html")

	res := call(srv, http.MethodGet, "/api/jobs/"+job.ID+"/file", cookie, nil, "")
	if ct := res.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content type %q, want text/plain; charset=utf-8", ct)
	}
	if cd := res.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("content disposition %q, want attachment", cd)
	}
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _, dir := newTestServerWithStore(t)
	return srv, dir
}

func newTestServerWithStore(t *testing.T) (*Server, *mem, string) {
	t.Helper()
	dir := t.TempDir()
	store := newMem()
	return New(store, dir, ""), store, dir
}

func accountID(t *testing.T, srv *Server, cookie string) string {
	t.Helper()
	res := call(srv, http.MethodGet, "/api/me", cookie, nil, "")
	var body account
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &body) != nil {
		t.Fatalf("me %d: %s", res.Code, res.Body.String())
	}
	return body.ID
}

func signUp(t *testing.T, srv *Server, email string) string {
	t.Helper()
	body := strings.NewReader(`{"email":"` + email + `","password":"password123"}`)
	res := call(srv, http.MethodPost, "/api/register", "", body, "application/json")
	if res.Code != http.StatusCreated {
		t.Fatalf("register %d: %s", res.Code, res.Body.String())
	}
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie.Value
		}
	}
	t.Fatal("register did not set a session")
	return ""
}

func createSheet(t *testing.T, srv *Server, cookie, title, address string) board.Job {
	t.Helper()
	body := strings.NewReader(`{"title":"` + title + `","address":"` + address + `"}`)
	res := call(srv, http.MethodPost, "/api/jobs", cookie, body, "application/json")
	if res.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", res.Code, res.Body.String())
	}
	return decodeJob(t, res)
}

func readySheet(t *testing.T, srv *Server, cookie, title string) board.Job {
	t.Helper()
	job := createSheet(t, srv, cookie, title, "10 Market Street")
	return uploadFile(t, srv, cookie, job.ID, "brochure.pdf", []byte("%PDF-1.4\n"), "application/pdf")
}

func uploadFile(t *testing.T, srv *Server, cookie, id, name string, data []byte, contentType string) board.Job {
	t.Helper()
	res := postFile(srv, cookie, id, name, data, contentType)
	if res.Code != http.StatusOK {
		t.Fatalf("upload %d: %s", res.Code, res.Body.String())
	}
	return decodeJob(t, res)
}

func postFile(srv *Server, cookie, id, name string, data []byte, contentType string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	header.Set("Content-Type", contentType)
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(data)
	_ = writer.Close()
	return call(srv, http.MethodPost, "/api/jobs/"+id+"/file", cookie, &buf, writer.FormDataContentType())
}

func startSheet(t *testing.T, srv *Server, cookie, id string, want int) board.Job {
	t.Helper()
	res := call(srv, http.MethodPost, "/api/jobs/"+id+"/start", cookie, nil, "")
	if res.Code != want {
		t.Fatalf("start %d, want %d: %s", res.Code, want, res.Body.String())
	}
	if want != http.StatusOK {
		return board.Job{}
	}
	return decodeJob(t, res)
}

func getSheet(t *testing.T, srv *Server, cookie, id string) board.Job {
	t.Helper()
	res := call(srv, http.MethodGet, "/api/jobs/"+id, cookie, nil, "")
	if res.Code != http.StatusOK {
		t.Fatalf("get %d: %s", res.Code, res.Body.String())
	}
	return decodeJob(t, res)
}

func errorMessage(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Error
}

func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func decodeJob(t *testing.T, res *httptest.ResponseRecorder) board.Job {
	t.Helper()
	var job board.Job
	if err := json.Unmarshal(res.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func call(srv *Server, method, path, cookie string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

type mem struct {
	mu       sync.Mutex
	users    map[string]board.User
	byEmail  map[string]string
	sessions map[string]memSession
	jobs     map[string]board.Job
}

type memSession struct {
	userID  string
	expires time.Time
}

func newMem() *mem {
	return &mem{
		users:    map[string]board.User{},
		byEmail:  map[string]string{},
		sessions: map[string]memSession{},
		jobs:     map[string]board.Job{},
	}
}

func (m *mem) CreateUser(_ context.Context, email, passwordHash string) (board.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byEmail[email]; ok {
		return board.User{}, board.ErrDuplicate
	}
	user := board.User{ID: board.NewID(), Email: email, PasswordHash: passwordHash}
	m.users[user.ID] = user
	m.byEmail[email] = user.ID
	return user, nil
}

func (m *mem) UserByEmail(_ context.Context, email string) (board.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byEmail[email]
	if !ok {
		return board.User{}, board.ErrNotFound
	}
	return m.users[id], nil
}

func (m *mem) CreateSession(_ context.Context, userID, token string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = memSession{userID: userID, expires: expires}
	return nil
}

func (m *mem) DeleteSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

func (m *mem) UserByToken(_ context.Context, token string) (board.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[token]
	if !ok || time.Now().After(session.expires) {
		return board.User{}, board.ErrNotFound
	}
	user, ok := m.users[session.userID]
	if !ok {
		return board.User{}, board.ErrNotFound
	}
	return user, nil
}

func (m *mem) ListJobs(_ context.Context, userID string) ([]board.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var jobs []board.Job
	for _, job := range m.jobs {
		if job.UserID == userID {
			jobs = append(jobs, cloneJob(job))
		}
	}
	if jobs == nil {
		jobs = []board.Job{}
	}
	return jobs, nil
}

func (m *mem) CreateJob(_ context.Context, job board.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = cloneJob(job)
	return nil
}

func (m *mem) Job(_ context.Context, id string) (board.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return board.Job{}, board.ErrNotFound
	}
	return cloneJob(job), nil
}

func (m *mem) AttachBrochure(_ context.Context, id, filename string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || job.Filename != "" {
		return board.ErrConflict
	}
	job.Filename = filename
	job.UpdatedAt = at
	m.jobs[id] = job
	return nil
}

func (m *mem) StartJob(_ context.Context, id, runID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || job.Status != board.StatusDraft {
		return nil
	}
	for _, other := range m.jobs {
		if other.UserID == job.UserID && other.Status == board.StatusRunning {
			return board.ErrConflict
		}
	}
	job.Status = board.StatusRunning
	job.RunID = runID
	job.UpdatedAt = at
	m.jobs[id] = job
	return nil
}

func (m *mem) RunningJobIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, job := range m.jobs {
		if job.Status == board.StatusRunning {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *mem) UpdateRun(_ context.Context, job board.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.jobs[job.ID]
	if !ok || current.Status != board.StatusRunning || current.RunID != job.RunID {
		return board.ErrConflict
	}
	m.jobs[job.ID] = cloneJob(job)
	return nil
}

func (m *mem) setStatus(id, status, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	job.Status = status
	job.Error = message
	m.jobs[id] = job
}

func cloneJob(job board.Job) board.Job {
	job.Checklist = append([]board.Step(nil), job.Checklist...)
	return job
}
