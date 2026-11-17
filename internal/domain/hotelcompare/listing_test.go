package hotelcompare

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"

	"github.com/FACorreiaa/loci-connect-api/pkg/safefetch"
)

const barcelonaMessage = "About my staying in Barcelona, what do you think about https://www.booking.com/Share-gpcrrRv 385 euros with breakfast vs https://www.booking.com/Share-6nDTCI 349 without breakfast. Also check the neighbouring and transportation"

func TestFindLinksKeepsWhatWasSaidAboutEach(t *testing.T) {
	got := FindLinks(barcelonaMessage)
	if len(got) != 2 {
		t.Fatalf("links = %+v", got)
	}
	if got[0].URL != "https://www.booking.com/Share-gpcrrRv" || got[0].Note != "385 euros with breakfast" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].URL != "https://www.booking.com/Share-6nDTCI" || got[1].Note != "349 without breakfast" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestFindLinks(t *testing.T) {
	cases := []struct {
		name, text string
		urls       []string
		notes      []string
	}{
		{"none", "where should I stay in Barcelona?", nil, nil},
		{"other sites are not listings", "see https://example.com/hotel and https://maps.app.goo.gl/abc", nil, nil},
		{"a lookalike host is not the site", "https://booking.com.evil.example/Share-x and https://notbooking.com/Share-y", nil, nil},
		{"no scheme, trailing punctuation", "is www.booking.com/Share-abc1, ok?", []string{"https://www.booking.com/Share-abc1"}, []string{"ok"}},
		{"http is upgraded", "http://booking.com/hotel/pt/x.html", []string{"https://booking.com/hotel/pt/x.html"}, []string{""}},
		{"repeat counted once", "https://www.booking.com/Share-a or https://www.booking.com/Share-a?x=1", []string{"https://www.booking.com/Share-a"}, []string{""}},
		{"decimal price survives the full stop rule", "https://www.booking.com/Share-a 385.50 a night. Thoughts", []string{"https://www.booking.com/Share-a"}, []string{"385.50 a night"}},
		{
			"at most three",
			"https://www.booking.com/Share-a https://www.booking.com/Share-b https://www.booking.com/Share-c https://www.booking.com/Share-d",
			[]string{"https://www.booking.com/Share-a", "https://www.booking.com/Share-b", "https://www.booking.com/Share-c"},
			[]string{"", "", ""},
		},
	}
	for _, c := range cases {
		got := FindLinks(c.text)
		if len(got) != len(c.urls) {
			t.Errorf("%s: links = %+v", c.name, got)
			continue
		}
		for i := range got {
			if got[i].URL != c.urls[i] || got[i].Note != c.notes[i] {
				t.Errorf("%s: link %d = %+v, want %s / %q", c.name, i, got[i], c.urls[i], c.notes[i])
			}
		}
	}
}

func TestBookingProperty(t *testing.T) {
	cases := map[string][3]string{
		"https://www.booking.com/hotel/es/verdi-254.en-gb.html":                  {"Verdi 254", "ES", "https://www.booking.com/hotel/es/verdi-254.html"},
		"https://www.booking.com/hotel/pt/casa-do-principe.html?aid=1&label=x":   {"Casa Do Principe", "PT", "https://www.booking.com/hotel/pt/casa-do-principe.html"},
		"https://www.booking.com/hotel/fr/le-meurice.fr.html":                    {"Le Meurice", "FR", "https://www.booking.com/hotel/fr/le-meurice.html"},
		"https://www.booking.com/hotel/br/copacabana-palace.pt-br.html#tab-main": {"Copacabana Palace", "BR", "https://www.booking.com/hotel/br/copacabana-palace.html"},
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		name, cc, page, ok := booking{}.property(u)
		if !ok || name != want[0] || cc != want[1] || page != want[2] {
			t.Errorf("%s = %q %q %q ok=%v", raw, name, cc, page, ok)
		}
	}
	for _, raw := range []string{
		"https://www.booking.com/Share-gpcrrRv",
		"https://www.booking.com/searchresults.html?ss=Barcelona",
		"https://www.booking.com/hotel/es/",
		"https://www.booking.com/hotel/es/../../etc/passwd.html",
	} {
		u, _ := url.Parse(raw)
		if _, _, _, ok := (booking{}).property(u); ok {
			t.Errorf("%s read as a property", raw)
		}
	}
}

// fakeRedirects answers GetUntil from a table of hops, stopping where the
// caller's stop says, as safefetch does.
type fakeRedirects struct {
	hops map[string][]string
	err  error
	// Links are followed at once, as the real fetcher is asked to.
	mu        sync.Mutex
	requested []string
}

func (f *fakeRedirects) request(u string) {
	f.mu.Lock()
	f.requested = append(f.requested, u)
	f.mu.Unlock()
}

func (f *fakeRedirects) GetUntil(_ context.Context, raw string, stop func(*url.URL) bool) (safefetch.Page, error) {
	f.request(raw)
	if f.err != nil {
		return safefetch.Page{}, f.err
	}
	final := raw
	for _, hop := range f.hops[raw] {
		u, _ := url.Parse(hop)
		final = hop
		if stop(u) {
			return safefetch.Page{FinalURL: final}, nil
		}
		f.request(hop)
	}
	return safefetch.Page{FinalURL: final}, nil
}

func TestResolveFollowsTheShareLinkToTheProperty(t *testing.T) {
	f := &fakeRedirects{hops: map[string][]string{
		"https://www.booking.com/Share-gpcrrRv": {
			"https://www.booking.com/Share-gpcrrRv?x=1",
			"https://www.booking.com/hotel/es/verdi-254.en-gb.html?aid=304142",
		},
	}}
	got := Resolve(context.Background(), f, FindLinks(barcelonaMessage))
	if len(got) != 2 {
		t.Fatal(got)
	}
	if !got[0].Resolved || got[0].Name != "Verdi 254" || got[0].CountryCode != "ES" ||
		got[0].PageURL != "https://www.booking.com/hotel/es/verdi-254.html" || got[0].Source != "booking.com" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Note != "385 euros with breakfast" {
		t.Errorf("note lost: %+v", got[0])
	}
	// The second link has no hops in the table: it leads nowhere.
	if got[1].Resolved || got[1].Source != "booking.com" || got[1].Note != "349 without breakfast" {
		t.Errorf("second = %+v", got[1])
	}
	for _, r := range f.requested {
		if u, _ := url.Parse(r); u.Path == "/hotel/es/verdi-254.en-gb.html" {
			t.Errorf("the property's page was requested: %s", r)
		}
	}
}

func TestResolveDoesNotFollowOffTheSite(t *testing.T) {
	f := &fakeRedirects{hops: map[string][]string{
		"https://www.booking.com/Share-a": {
			"https://evil.example/hotel/es/verdi-254.html",
			"https://www.booking.com/hotel/es/verdi-254.html",
		},
	}}
	got := Resolve(context.Background(), f, []Link{{URL: "https://www.booking.com/Share-a"}})
	if got[0].Resolved {
		t.Errorf("resolved through another site: %+v", got[0])
	}
	if len(f.requested) != 1 {
		t.Errorf("requested %v, want only the link itself", f.requested)
	}
}

func TestResolveNeedsNoFetchForAPropertyLinkAndSurvivesFailure(t *testing.T) {
	f := &fakeRedirects{err: errors.New("blocked")}
	got := Resolve(context.Background(), f, []Link{
		{URL: "https://www.booking.com/hotel/pt/casa-do-principe.pt-pt.html"},
		{URL: "https://www.booking.com/Share-zzz"},
	})
	if !got[0].Resolved || got[0].Name != "Casa Do Principe" {
		t.Errorf("property link = %+v", got[0])
	}
	if got[1].Resolved {
		t.Errorf("failed fetch resolved: %+v", got[1])
	}
	if len(f.requested) != 1 {
		t.Errorf("requested %v, want only the share link", f.requested)
	}
	if nilFetch := Resolve(context.Background(), nil, []Link{{URL: "https://www.booking.com/Share-zzz"}}); nilFetch[0].Resolved {
		t.Error("resolved with no fetcher")
	}
}
