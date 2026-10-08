package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"streamuploader/drive/meta"
	"streamuploader/drive/sidecar"
)

type searchHit struct {
	File    fileView `json:"file"`
	Score   float64  `json:"score"`
	Page    int64    `json:"page,omitempty"`
	View    string   `json:"view,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	// Fresh marks a result that came from the journal overlay rather than
	// the index.
	Fresh bool `json:"fresh,omitempty"`
}

type searchResponse struct {
	Total   int         `json:"total"`
	Hits    []searchHit `json:"hits"`
	Query   string      `json:"query"`
	Facets  []string    `json:"facets"`
	Exact   bool        `json:"exact"`
	Sort    string      `json:"sort"`
	Overlay int         `json:"overlay"`
}

// listingFilter decides whether an overlay state belongs in a result set.
type listingFilter struct {
	query  string
	facets []string
	exact  bool
}

func (lf listingFilter) matches(f *meta.File) bool {
	if f.Deleted {
		return false
	}
	paths := sidecar.FacetPaths(f)
	for _, want := range lf.facets {
		found := false
		for _, p := range paths {
			if p == want || (!lf.exact && strings.HasPrefix(p, want+"/")) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if q := strings.ToLower(strings.TrimSpace(lf.query)); q != "" {
		// The index has not seen this file yet, so only its name and author
		// can be matched here.
		if !strings.Contains(strings.ToLower(f.Name), q) && !strings.Contains(strings.ToLower(f.Author.Effective()), q) {
			return false
		}
	}
	return true
}

func parseFacetParams(r *http.Request) ([]string, bool) {
	var facets []string
	for _, tag := range r.URL.Query()["tag"] {
		if t, err := meta.NormalizeTag(tag); err == nil {
			facets = append(facets, "/tags"+t)
		}
	}
	for _, facet := range r.URL.Query()["facet"] {
		facet = strings.TrimSpace(facet)
		if facet == "" {
			continue
		}
		if !strings.HasPrefix(facet, "/") {
			facet = "/" + facet
		}
		facets = append(facets, strings.TrimRight(facet, "/"))
	}
	exactParam := r.URL.Query().Get("exact")
	exact := len(facets) > 0 && strings.TrimSpace(r.URL.Query().Get("q")) == ""
	if exactParam != "" {
		exact = exactParam == "1" || exactParam == "true"
	}
	return facets, exact
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	facets, exact := parseFacetParams(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortKey == "" {
		if q != "" {
			sortKey = "score"
		} else {
			sortKey = "name"
		}
	}
	res, err := s.deps.Search.Search(ctx, sidecar.SearchRequest{
		Tenant: s.cfg.Tenant, Query: q, Facets: facets, Exact: exact, Sort: sortKey, Limit: limit, Offset: offset, WithPages: true,
	})
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	overlay, err := s.overlayStates(ctx)
	if err != nil {
		s.fail(w, "overlay", err)
		return
	}
	filter := listingFilter{query: q, facets: facets, exact: exact}
	hits := make([]searchHit, 0, len(res.Hits)+len(overlay))
	seen := map[string]bool{}
	dropped := 0
	for _, h := range res.Hits {
		seen[h.FileID] = true
		f, ok := overlay[h.FileID]
		fresh := ok
		if !ok {
			f, ok = s.deps.Metas.Get(h.FileID)
		}
		if !ok {
			dropped++
			continue
		}
		if fresh && !filter.matches(f) {
			// The journal moved or deleted the file after it was indexed.
			dropped++
			continue
		}
		if f.Deleted {
			dropped++
			continue
		}
		hits = append(hits, searchHit{File: s.view(f), Score: h.Score, Page: h.Page, View: h.View, Snippet: h.Snippet, Fresh: fresh})
	}
	added := 0
	if offset == 0 {
		ids := make([]string, 0, len(overlay))
		for id := range overlay {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			f := overlay[id]
			if !filter.matches(f) {
				continue
			}
			if _, indexed := s.deps.Metas.Get(id); indexed && q != "" {
				// Already indexed with other content; the index decided it
				// does not match the text query.
				continue
			}
			hits = append(hits, searchHit{File: s.view(f), Fresh: true})
			added++
		}
		if added > 0 && sortKey != "score" {
			sortHits(hits, sortKey)
		}
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Total:   res.Total - dropped + added,
		Hits:    hits,
		Query:   q,
		Facets:  nonNil(facets),
		Exact:   exact,
		Sort:    sortKey,
		Overlay: len(overlay),
	})
}

func sortHits(hits []searchHit, key string) {
	desc := strings.HasPrefix(key, "-")
	key = strings.TrimPrefix(key, "-")
	less := func(a, b *meta.File) bool {
		switch key {
		case "size":
			return a.SizeBytes < b.SizeBytes
		case "modified":
			return a.Dates.Modified.Before(b.Dates.Modified)
		case "uploaded":
			return a.Dates.Uploaded.Before(b.Dates.Uploaded)
		case "created":
			return a.PrimaryDate().Before(b.PrimaryDate())
		default:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i].File.File, hits[j].File.File
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
}

type facetChild struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type facetsResponse struct {
	Path     string       `json:"path"`
	Children []facetChild `json:"children"`
}

func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := strings.TrimRight(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	if path == "" {
		path = "/tags"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	facets, exact := parseFacetParams(r)
	res, err := s.deps.Search.Facets(ctx, sidecar.FacetsRequest{Tenant: s.cfg.Tenant, Path: path, Query: q, Facets: facets, Exact: exact})
	if err != nil {
		s.fail(w, "facets", err)
		return
	}
	counts := map[string]int64{}
	for _, c := range res.Children {
		counts[c.Path] = c.Count
	}
	// Patch the counts with files the indexer has not folded yet: subtract
	// what the index counted for the old state, add the new state.
	overlay, err := s.overlayStates(ctx)
	if err != nil {
		s.fail(w, "overlay", err)
		return
	}
	filter := listingFilter{query: q, facets: facets, exact: exact}
	for id, f := range overlay {
		if old, ok := s.cachedFile(id); ok && !old.Deleted && filter.matches(old) {
			for _, child := range childrenUnder(sidecar.FacetPaths(old), path) {
				counts[child]--
			}
		}
		if filter.matches(f) {
			for _, child := range childrenUnder(sidecar.FacetPaths(f), path) {
				counts[child]++
			}
		}
	}
	children := make([]facetChild, 0, len(counts))
	for p, n := range counts {
		if n <= 0 {
			continue
		}
		children = append(children, facetChild{Path: p, Name: p[strings.LastIndex(p, "/")+1:], Count: n})
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Path < children[j].Path })
	writeJSON(w, http.StatusOK, facetsResponse{Path: path, Children: children})
}

// childrenUnder maps a file's facet paths to the direct children of path they
// fall under, without duplicates.
func childrenUnder(paths []string, parent string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		rest, ok := strings.CutPrefix(p, parent+"/")
		if !ok || rest == "" {
			continue
		}
		seg := rest
		if i := strings.Index(rest, "/"); i >= 0 {
			seg = rest[:i]
		}
		child := parent + "/" + seg
		if !seen[child] {
			seen[child] = true
			out = append(out, child)
		}
	}
	return out
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
