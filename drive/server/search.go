package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/drive/meta"
	"streamuploader/drive/sidecar"
)

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

// facetParams turns the tag and facet query values into facet paths, and
// decides whether they must match exactly: by default a plain listing (facets
// and no text query) is exact, and the exact parameter overrides that.
func facetParams(tags, facets []string, exactParam, q string) ([]string, bool) {
	var out []string
	for _, tag := range tags {
		if t, err := meta.NormalizeTag(tag); err == nil {
			out = append(out, "/tags"+t)
		}
	}
	for _, facet := range facets {
		facet = strings.TrimSpace(facet)
		if facet == "" {
			continue
		}
		if !strings.HasPrefix(facet, "/") {
			facet = "/" + facet
		}
		out = append(out, strings.TrimRight(facet, "/"))
	}
	exact := len(out) > 0 && strings.TrimSpace(q) == ""
	if exactParam != "" {
		exact = exactParam == "1" || exactParam == "true"
	}
	return out, exact
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, err := pw.Parse[searchInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	q := strings.TrimSpace(in.Q)
	facets, exact := facetParams(in.Tag, in.Facet, in.Exact, q)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	sortKey := strings.TrimSpace(in.Sort)
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
		s.fail(w, r, "search", err)
		return
	}
	overlay, err := s.overlayStates(ctx)
	if err != nil {
		s.fail(w, r, "overlay", err)
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
	pw.WriteAPI(w, r, searchResponse{
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
		a, b := hits[i].File.file, hits[j].File.file
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
}

func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, err := pw.Parse[facetsInput](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	path := strings.TrimRight(strings.TrimSpace(in.Path), "/")
	if path == "" {
		path = "/tags"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	q := strings.TrimSpace(in.Q)
	facets, exact := facetParams(in.Tag, in.Facet, in.Exact, q)
	res, err := s.deps.Search.Facets(ctx, sidecar.FacetsRequest{Tenant: s.cfg.Tenant, Path: path, Query: q, Facets: facets, Exact: exact})
	if err != nil {
		s.fail(w, r, "facets", err)
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
		s.fail(w, r, "overlay", err)
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
	pw.WriteAPI(w, r, facetsResponse{Path: path, Children: children})
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
