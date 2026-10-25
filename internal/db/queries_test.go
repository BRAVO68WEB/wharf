package db

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestQueries_RepositoryRoundtrip: Given a migrated database, When a repo is
// inserted through the generated query and read back, Then the stored row
// matches. This is the plan's happy-path QA scenario.
func TestQueries_RepositoryRoundtrip(t *testing.T) {
	q := New(newTestDB(t))
	ctx := t.Context()

	created, err := q.CreateRepository(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if created.Name != "library/nginx" {
		t.Errorf("created name = %q, want library/nginx", created.Name)
	}
	if created.ID == 0 {
		t.Error("created id = 0, want non-zero")
	}

	got, err := q.GetRepositoryByName(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("GetRepositoryByName: %v", err)
	}
	if got != created {
		t.Errorf("GetRepositoryByName = %+v, want %+v", got, created)
	}

	// Upsert semantics: same name yields the same row, not a duplicate.
	again, err := q.CreateRepository(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("CreateRepository (upsert): %v", err)
	}
	if again.ID != created.ID {
		t.Errorf("upsert id = %d, want %d", again.ID, created.ID)
	}
	if created.Published {
		t.Error("new repository is published")
	}
	n, err := q.SetRepositoryPublished(ctx, SetRepositoryPublishedParams{Published: true, Name: "library/nginx"})
	if err != nil {
		t.Fatalf("SetRepositoryPublished: %v", err)
	}
	if n != 1 {
		t.Fatalf("SetRepositoryPublished rows = %d, want 1", n)
	}
	kept, err := q.CreateRepository(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("CreateRepository after publish: %v", err)
	}
	if !kept.Published {
		t.Error("push cleared the published flag")
	}

	n, err = q.CountRepositories(ctx)
	if err != nil {
		t.Fatalf("CountRepositories: %v", err)
	}
	if n != 1 {
		t.Errorf("CountRepositories = %d, want 1", n)
	}

	if _, err := q.GetRepositoryByName(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetRepositoryByName(missing) = %v, want sql.ErrNoRows", err)
	}
}

// TestQueries_ManifestAndTagRoundtrip: Given a repository, When a manifest and
// a tag are written and fetched through the generated queries, Then the
// payload survives byte-for-byte and tag lookup joins to the manifest.
func TestQueries_ManifestAndTagRoundtrip(t *testing.T) {
	sqlDB := newTestDB(t)
	q := New(sqlDB)
	ctx := t.Context()

	repo, err := q.CreateRepository(ctx, "app/api")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	const digest = "sha256:deadbeef"
	payload := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	if err := q.UpsertManifest(ctx, UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload:   payload,
		Size:      int64(len(payload)),
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}

	manifest, err := q.GetManifest(ctx, GetManifestParams{RepoID: repo.ID, Digest: digest})
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if string(manifest.Payload) != string(payload) {
		t.Errorf("payload = %s, want %s", manifest.Payload, payload)
	}
	if manifest.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", manifest.Size, len(payload))
	}

	now := time.Now().Unix()
	if err := q.SetTag(ctx, SetTagParams{
		RepoID:         repo.ID,
		Name:           "latest",
		ManifestDigest: digest,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	byTag, err := q.GetManifestByTag(ctx, GetManifestByTagParams{RepoID: repo.ID, Name: "latest"})
	if err != nil {
		t.Fatalf("GetManifestByTag: %v", err)
	}
	if byTag.Digest != digest {
		t.Errorf("GetManifestByTag digest = %q, want %q", byTag.Digest, digest)
	}

	// Retagging must move the tag, not create a second one.
	const digest2 = "sha256:cafebabe"
	if err := q.UpsertManifest(ctx, UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest2,
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload:   payload,
		Size:      int64(len(payload)),
	}); err != nil {
		t.Fatalf("UpsertManifest (second): %v", err)
	}
	if err := q.SetTag(ctx, SetTagParams{
		RepoID:         repo.ID,
		Name:           "latest",
		ManifestDigest: digest2,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("SetTag (retag): %v", err)
	}
	tag, err := q.GetTag(ctx, GetTagParams{RepoID: repo.ID, Name: "latest"})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if tag.ManifestDigest != digest2 {
		t.Errorf("retagged manifest = %q, want %q", tag.ManifestDigest, digest2)
	}
	if n, err := q.CountTagsByRepo(ctx, repo.ID); err != nil || n != 1 {
		t.Errorf("CountTagsByRepo = %d, %v; want 1, nil", n, err)
	}
}

// TestQueries_BlobAndUploadRoundtrip: Given a repository, When blob metadata,
// refs and an upload session are written and read, Then every field roundtrips
// (including the quoted "offset" column).
func TestQueries_BlobAndUploadRoundtrip(t *testing.T) {
	q := New(newTestDB(t))
	ctx := t.Context()

	repo, err := q.CreateRepository(ctx, "app/api")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	const digest = "sha256:abc123"
	if err := q.UpsertBlobMetadata(ctx, UpsertBlobMetadataParams{
		Digest:    digest,
		Size:      4096,
		MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
	}); err != nil {
		t.Fatalf("UpsertBlobMetadata: %v", err)
	}
	meta, err := q.GetBlobMetadata(ctx, digest)
	if err != nil {
		t.Fatalf("GetBlobMetadata: %v", err)
	}
	if meta.Size != 4096 {
		t.Errorf("blob size = %d, want 4096", meta.Size)
	}

	if err := q.AddBlobRef(ctx, AddBlobRefParams{Digest: digest, RepoID: repo.ID}); err != nil {
		t.Fatalf("AddBlobRef: %v", err)
	}
	if err := q.AddBlobRef(ctx, AddBlobRefParams{Digest: digest, RepoID: repo.ID}); err != nil {
		t.Fatalf("AddBlobRef (idempotent): %v", err)
	}
	if n, err := q.CountBlobRefs(ctx); err != nil || n != 1 {
		t.Errorf("CountBlobRefs = %d, %v; want 1, nil", n, err)
	}

	now := time.Now().Unix()
	params := CreateUploadParams{
		Uuid:      "00000000-0000-4000-8000-000000000001",
		RepoID:    repo.ID,
		Digest:    "",
		Offset:    0,
		Size:      0,
		TempPath:  "/tmp/upload-1",
		CreatedAt: now,
		ExpiresAt: now + 3600,
	}
	if err := q.CreateUpload(ctx, params); err != nil {
		t.Fatalf("CreateUpload: %v", err)
	}

	const newOffset = 1024
	if err := q.UpdateUploadProgress(ctx, UpdateUploadProgressParams{
		Uuid:   params.Uuid,
		Digest: digest,
		Offset: newOffset,
		Size:   newOffset,
	}); err != nil {
		t.Fatalf("UpdateUploadProgress: %v", err)
	}

	upload, err := q.GetUpload(ctx, params.Uuid)
	if err != nil {
		t.Fatalf("GetUpload: %v", err)
	}
	if upload.Offset != newOffset {
		t.Errorf("upload offset = %d, want %d", upload.Offset, newOffset)
	}
	if upload.Digest != digest {
		t.Errorf("upload digest = %q, want %q", upload.Digest, digest)
	}
	if upload.TempPath != params.TempPath {
		t.Errorf("upload temp_path = %q, want %q", upload.TempPath, params.TempPath)
	}

	expired, err := q.ListExpiredUploads(ctx, now+7200)
	if err != nil {
		t.Fatalf("ListExpiredUploads: %v", err)
	}
	if len(expired) != 1 {
		t.Errorf("ListExpiredUploads = %d rows, want 1", len(expired))
	}
}

// TestQueries_UsersSessionsAudit: Given a user, When a session and audit entry
// reference it, Then the generated queries roundtrip booleans and nullable
// user references; deleting the user cascades to sessions but keeps the audit
// row with a NULL user_id.
func TestQueries_UsersSessionsAudit(t *testing.T) {
	q := New(newTestDB(t))
	ctx := t.Context()

	user, err := q.CreateUser(ctx, CreateUserParams{
		Username:     "admin",
		PasswordHash: "$2a$10$fakefakefakefakefakefake",
		IsAdmin:      true,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !user.IsAdmin {
		t.Error("IsAdmin = false, want true (bool override broken?)")
	}

	fetched, err := q.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if fetched.ID != user.ID || !fetched.IsAdmin {
		t.Errorf("GetUserByUsername = %+v, want %+v", fetched, user)
	}

	now := time.Now().Unix()
	if err := q.CreateSession(ctx, CreateSessionParams{
		Token:     "session-token-1",
		UserID:    user.ID,
		ExpiresAt: now + 3600,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := q.GetSession(ctx, "session-token-1"); err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if err := q.InsertAuditEntry(ctx, InsertAuditEntryParams{
		Action:    "repository.delete",
		UserID:    sql.NullInt64{Int64: user.ID, Valid: true},
		Target:    "app/api",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertAuditEntry: %v", err)
	}

	if err := q.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := q.GetSession(ctx, "session-token-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("session after user delete = %v, want sql.ErrNoRows (cascade)", err)
	}

	entries, err := q.ListAuditEntriesFromStart(ctx, 10)
	if err != nil {
		t.Fatalf("ListAuditEntriesFromStart: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 (row survives user delete)", len(entries))
	}
	if entries[0].UserID.Valid {
		t.Errorf("audit user_id = %+v, want NULL after user delete", entries[0].UserID)
	}
}
