package pages

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"scilla/internal/panel/addons"
	"scilla/internal/panel/server"
)

// The public pages of an addon of protocol 2: its /v2/info names an entry under /ui/ and
// the admin approved the ui:public permission, the panel serves the files below the
// subscription path to anyone, without a session:
//
//	/<sub path>/addons-public/<id>/<file>
//
// They are for the panel's users, not its admins: a status page, a knowledge base. The
// files are static on purpose: PublicCSP allows no scripts, so a page cannot act for a
// visitor beyond what plain HTML and CSS do, and the panel's cookies stay out of reach.
// What an addon may ask its pages is still its granted permissions; a public page has no
// way to call the panel's API unless the addon answers such calls itself.

const (
	// PublicPermission is the grant an addon needs for pages every visitor may open.
	PublicPermission = "ui:public"
	// PublicPrefix is where the public pages live, below the subscription path.
	PublicPrefix = "/addons-public/"
)

// PublicCSP is the policy of an addon's public pages: no scripts, no objects, no forms;
// styles inline or from the panel's own origin, images from the panel or data: URLs.
const PublicCSP = "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"

// Public serves the pages of an addon the admin approved for everyone.
type Public struct {
	d Deps
	// The links the subscription page shows, kept for a few seconds: it asks on every
	// view, while installs and removals of addons are rare.
	mu      sync.Mutex
	linksAt time.Time
	links   []Link
}

func NewPublic(d Deps) *Public { return &Public{d: d} }

// Link is a public page of an installed addon, for the subscription page to link to.
type Link struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// linksTTL is how long the list of public pages is kept.
const linksTTL = 10 * time.Second

// Links lists the addons whose public pages visitors may open, each with the address of
// its entry below the subscription path. An empty subBase means the panel has no
// subscription path, so no public page is reachable and there is nothing to link.
func (p *Public) Links(ctx context.Context, subBase string) ([]Link, error) {
	if p == nil || p.d.Addons == nil || subBase == "" {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.links != nil && p.d.Now().Sub(p.linksAt) < linksTTL {
		return p.links, nil
	}
	st, err := p.d.Addons.State()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(st.Adapters))
	for id := range st.Adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []Link{}
	for _, id := range ids {
		a, err := p.allowed(ctx, id)
		if err != nil {
			continue
		}
		info, err := p.d.Addons.InfoV2(ctx, id, a)
		if err != nil || info.UI == nil {
			continue
		}
		out = append(out, Link{ID: id, Title: pickText(info.UI.Title), URL: subBase + PublicPrefix + id + "/"})
	}
	p.links, p.linksAt = out, p.d.Now()
	return out, nil
}

// pickText is the addon's title in English when it has one, in any of its languages last.
func pickText(t addons.Text) string {
	if s := t["en"]; s != "" {
		return s
	}
	for _, s := range t {
		return s
	}
	return ""
}

// allowed is Allowed for the public permission: the addon runs, serves HTTP, speaks
// protocol 2 and the admin approved ui:public.
func (p *Public) allowed(ctx context.Context, id string) (addons.Installed, error) {
	if p == nil || p.d.Addons == nil {
		return addons.Installed{}, ErrNoPages
	}
	a, err := p.d.Addons.Served(id)
	if errors.Is(err, addons.ErrNotInstalled) {
		return a, ErrNoPages
	}
	if err != nil {
		return a, err
	}
	perms, _, err := addons.LoadGrant(ctx, p.d.Store, id)
	if errors.Is(err, addons.ErrNoGrant) || err == nil && !addons.HasPerm(perms, PublicPermission) {
		return a, ErrForbidden
	}
	return a, err
}

// open answers the addon's entry when the address names no file.
func (p *Public) entry(ctx context.Context, a addons.Installed, id string) (string, error) {
	info, err := p.d.Addons.InfoV2(ctx, id, a)
	if err != nil {
		return "", err
	}
	if info.UI == nil {
		return "", ErrNoPages
	}
	return strings.TrimPrefix(info.UI.Entry, "/ui/"), nil
}

// ServeHTTP serves a file of the public pages. Anything wrong is the panel's bare 404:
// an addon that is not installed, not running or not approved looks like any unknown
// path, so the address itself tells a visitor nothing.
func (p *Public) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, PublicPrefix)
	id, name, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || !addons.ValidID(id) {
		server.NotFound(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	a, err := p.allowed(ctx, id)
	if err != nil {
		server.NotFound(w)
		return
	}
	if name == "" {
		if name, err = p.entry(ctx, a, id); err != nil {
			server.NotFound(w)
			return
		}
	}
	h := w.Header()
	publicHeaders(h)
	f, err := p.d.Addons.Page(ctx, a, name, r.URL.RawQuery, r.Header)
	if err != nil {
		p.d.Log.Warn("addon public pages: file", "addon", id, "err", err)
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("502 the addon does not answer\n"))
		return
	}
	for k, v := range f.Header {
		h[k] = v
	}
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "public, max-age=30")
	}
	if f.Status == http.StatusNotModified {
		w.WriteHeader(f.Status)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(f.Body)))
	w.WriteHeader(f.Status)
	_, _ = w.Write(f.Body)
}

// publicHeaders replaces the panel's security headers for an addon's public page: the
// page is static (PublicCSP allows no scripts), so it cannot reach the panel's cookies
// or act as the visitor; framing it is of no use to anyone.
func publicHeaders(h http.Header) {
	h.Set("Content-Security-Policy", PublicCSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}
