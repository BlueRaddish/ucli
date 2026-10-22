package connections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type record struct {
	Format     int        `json:"format"`
	Revision   uint64     `json:"revision"`
	Removed    bool       `json:"removed"`
	Connection Connection `json:"connection"`
}
type envelope struct {
	Record record `json:"record"`
	SHA256 string `json:"sha256"`
}

func (s *Service) records(ctx context.Context) ([]record, error) {
	if ctx == nil {
		return nil, problem("connection_input", "Connections require a context.")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkChain(s.directory); err != nil {
		return nil, err
	}
	dir, err := os.Open(s.directory)
	if os.IsNotExist(err) {
		return []record{}, nil
	}
	if err != nil {
		return nil, problem("connection_store", "Connection metadata could not be opened.")
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1025)
	if err != nil && err != io.EOF || len(entries) > 1024 {
		return nil, problem("connection_store", "Connection history exceeds its 1024-entry bound. Preserve it for inspection.")
	}
	latest := map[string]record{}
	total := int64(0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if name == ".lock" || strings.HasPrefix(name, ".pending-") {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			return nil, problem("connection_corrupt", "An unrecognized file is present in connection history; it was kept.")
		}
		path := join(s.directory, name)
		if err := checkChain(path); err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<10 {
			return nil, problem("connection_corrupt", "Connection metadata is linked, oversized or unreadable; it was kept.")
		}
		total += info.Size()
		if total > 16<<20 {
			return nil, problem("connection_store", "Connection metadata exceeds its 16 MiB read bound.")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, problem("connection_store", "Connection metadata could not be read.")
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, (64<<10)+1))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || len(raw) > 64<<10 {
			return nil, problem("connection_store", "Connection metadata could not be read within its bounds.")
		}
		var e envelope
		if json.Unmarshal(raw, &e) != nil {
			return nil, problem("connection_corrupt", "Connection metadata is invalid; it was kept.")
		}
		canonical, _ := json.Marshal(e)
		body, _ := json.Marshal(e.Record)
		sum := sha256.Sum256(body)
		if !bytes.Equal(raw, canonical) || e.SHA256 != hex.EncodeToString(sum[:]) || !validRecord(e.Record) || name != recordName(e.Record) {
			return nil, problem("connection_corrupt", "Connection history failed its format, identity or checksum check; it was kept.")
		}
		prior, exists := latest[e.Record.Connection.ID]
		if !exists || e.Record.Revision > prior.Revision {
			latest[e.Record.Connection.ID] = e.Record
		}
	}
	result := []record{}
	labels := map[string]bool{}
	homes := map[string]bool{}
	for _, r := range latest {
		if !r.Removed {
			key := r.Connection.Provider + "\x00" + pathKey(r.Connection.NativeHome)
			if labels[r.Connection.Label] || homes[key] {
				return nil, problem("connection_corrupt", "Connection history contains conflicting labels or native homes.")
			}
			labels[r.Connection.Label], homes[key] = true, true
		}
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Connection.ID < result[j].Connection.ID })
	return result, nil
}
func recordName(r record) string { return fmt.Sprintf("%s.%020d.json", r.Connection.ID, r.Revision) }
func validRecord(r record) bool {
	c := r.Connection
	return validFormat(r) && r.Revision > 0 && validID(c.ID) && validText(c.Label, 128) && c.Label != "" && !validID(c.Label) && providerKnown(c.Provider) && filepath.IsAbs(c.Executable) && filepath.IsAbs(c.NativeHome) && validText(c.Executable, 32768) && validText(c.NativeHome, 32768) && validHash(c.SHA256) && (c.Identity == "" || validHash(c.Identity)) && (c.HomeMode == "existing" || c.HomeMode == "separate") && (c.AuthState == "unknown" || c.AuthState == "authenticated" || c.AuthState == "signed-out" || c.AuthState == "identity-changed") && validText(c.AuthMode, 64) && validText(c.Version, 128) && validText(c.PlanType, 64) && validModels(c.Models) && (c.Compatibility == "" || c.Compatibility == "unknown" || c.Compatibility == "reviewed") && (c.CatalogSHA256 == "" || validHash(c.CatalogSHA256)) && c.Isolation == "unverified" && !c.ManagedAI && !c.ObservedAt.IsZero()
}

// Format 1 remains byte-for-byte readable. Extended evidence is written only
// as format 2; older binaries must refuse it rather than ignore admission data.
func validFormat(r record) bool {
	if r.Format == 2 {
		return true
	}
	if r.Format != 1 || r.Connection.Compatibility != "" || r.Connection.CatalogSHA256 != "" {
		return false
	}
	for _, m := range r.Connection.Models {
		if m.ResolvedID != "" || m.EffortState != "" || m.Unavailable {
			return false
		}
	}
	return true
}
func validModels(models []Model) bool {
	if len(models) > 256 {
		return false
	}
	seen := map[string]bool{}
	defaults := 0
	for _, m := range models {
		if !validText(m.ResolvedID, 256) || (m.EffortState != "" && m.EffortState != "unknown" && m.EffortState != "supported" && m.EffortState != "unsupported") || (m.EffortState == "unsupported" && len(m.Efforts) > 0) || (m.EffortState == "supported" && len(m.Efforts) == 0) || m.ID == "" || !validText(m.ID, 256) || !validText(m.Name, 256) || !validText(m.DefaultEffort, 32) || len(m.Efforts) > 16 || seen[m.ID] {
			return false
		}
		seen[m.ID] = true
		if m.Default {
			defaults++
		}
		for _, e := range m.Efforts {
			if !validText(e, 32) || e == "" {
				return false
			}
		}
	}
	return defaults <= 1
}
func validID(v string) bool {
	return strings.HasPrefix(v, "connection_") && len(v) == len("connection_")+32 && validHex(strings.TrimPrefix(v, "connection_"))
}
func validHex(v string) bool {
	_, err := hex.DecodeString(v)
	return err == nil && strings.ToLower(v) == v
}
func validHash(v string) bool { return len(v) == 64 && validHex(v) }
func (s *Service) append(ctx context.Context, c Connection, removed bool) error {
	records, err := s.records(ctx)
	if err != nil {
		return err
	}
	r := record{Format: 2, Revision: 1, Removed: removed, Connection: c}
	for _, old := range records {
		if old.Connection.ID == c.ID {
			if old.Revision == ^uint64(0) {
				return problem("connection_store", "Connection revision limit reached.")
			}
			r.Revision = old.Revision + 1
		}
	}
	if !validRecord(r) {
		return problem("connection_corrupt", "Connection metadata cannot be published because its fields are invalid.")
	}
	body, _ := json.Marshal(r)
	sum := sha256.Sum256(body)
	raw, _ := json.Marshal(envelope{r, hex.EncodeToString(sum[:])})
	if len(raw) > 64<<10 {
		return problem("connection_store", "Connection metadata exceeds its publication bound.")
	}
	if err = s.publicationFits(ctx, len(raw)); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.directory, ".pending-")
	if err != nil {
		return problem("connection_store", "Connection metadata could not be staged.")
	}
	name := file.Name()
	defer os.Remove(name)
	if err = secureCreatedPath(name, false); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return problem("connection_store", "Connection metadata could not be secured or flushed.")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = checkChain(s.directory); err != nil {
		return err
	}
	if err = os.Link(name, join(s.directory, recordName(r))); err != nil {
		return problem("connection_store", "Connection history could not be published without overwriting; existing records were kept.")
	}
	return nil
}

// Called under the mutation lock, after records validated the existing files.
// Leave room for both the private stage and its published link so concurrent
// passive readers also stay within the same bounded directory scan.
func (s *Service) publicationFits(ctx context.Context, bytes int) error {
	dir, err := os.Open(s.directory)
	if err != nil {
		return problem("connection_store", "Connection history could not be checked before publication.")
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1023)
	if err != nil && err != io.EOF || len(entries) >= 1023 {
		return problem("connection_store", "Connection history has reached its entry bound. Prior records remain readable; preserve them for migration.")
	}
	total := int64(bytes)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == ".lock" || strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return problem("connection_store", "Connection metadata size could not be checked.")
		}
		total += info.Size()
		if total > 16<<20 {
			return problem("connection_store", "Connection history has reached its byte bound. Prior records remain readable; preserve them for migration.")
		}
	}
	return nil
}
