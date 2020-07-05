// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package goatcounter

import (
	"context"
	"net/url"
	"strings"
	"time"

	"zgo.at/errors"
	"zgo.at/zdb"
)

// ref_scheme column
var (
	RefSchemeHTTP      = ptr("h")
	RefSchemeOther     = ptr("o")
	RefSchemeGenerated = ptr("g")
	RefSchemeCampaign  = ptr("c")
)

var groups = map[string]string{
	// HN has <meta name="referrer" content="origin"> so we only get the domain.
	"news.ycombinator.com":               "Hacker News",
	"hn.algolia.com":                     "Hacker News",
	"hckrnews.com":                       "Hacker News",
	"hn.premii.com":                      "Hacker News",
	"com.stefandekanski.hackernews.free": "Hacker News",
	"io.github.hidroh.materialistic":     "Hacker News",
	"hackerweb.app":                      "Hacker News",
	"www.daemonology.net/hn-daily":       "Hacker News",
	"quiethn.com":                        "Hacker News",
	"hnews.xyz":                          "Hacker News",
	"hackernewsmobile.com":               "Hacker News",
	// http://www.elegantreader.com/item/17358103
	// https://www.daemonology.net/hn-daily/2019-05.html

	"mail.google.com":       "Email",
	"com.google.android.gm": "Email",
	"mail.yahoo.com":        "Email",
	//  https://mailchi.mp

	"org.fox.ttrss":            "RSS",
	"www.inoreader.com":        "RSS",
	"com.innologica.inoreader": "RSS",
	"usepanda.com":             "RSS",
	"feedly.com":               "RSS",

	"com.google.android.googlequicksearchbox":                      "Google",
	"com.google.android.googlequicksearchbox/https/www.google.com": "Google",

	"com.andrewshu.android.reddit":       "www.reddit.com",
	"com.laurencedawson.reddit_sync":     "www.reddit.com",
	"com.laurencedawson.reddit_sync.dev": "www.reddit.com",
	"com.laurencedawson.reddit_sync.pro": "www.reddit.com",

	"m.facebook.com":  "www.facebook.com",
	"l.facebook.com":  "www.facebook.com",
	"lm.facebook.com": "www.facebook.com",

	"org.telegram.messenger": "Telegram Messenger",

	"com.Slack": "Slack Chat",
}

var hostAlias = map[string]string{
	"en.m.wikipedia.org": "en.wikipedia.org",
	"m.facebook.com":     "www.facebook.com",
	"m.habr.com":         "habr.com",
	"old.reddit.com":     "www.reddit.com",
	"i.reddit.com":       "www.reddit.com",
	"np.reddit.com":      "www.reddit.com",
	"fr.reddit.com":      "www.reddit.com",
}

func cleanRefURL(ref string, refURL *url.URL) (string, bool) {
	// I'm not sure where these links are generated, but there are *a lot* of
	// them.
	if refURL.Host == "link.oreilly.com" {
		return "link.oreilly.com", false
	}

	// Always remove protocol.
	refURL.Scheme = ""
	if p := strings.Index(ref, ":"); p > -1 && p < 7 {
		ref = ref[p+3:]
	}

	// Normalize some hosts.
	if a, ok := hostAlias[refURL.Host]; ok {
		refURL.Host = a
	}

	// Group based on URL.
	if strings.HasPrefix(refURL.Host, "www.google.") {
		// Group all "google.co.nz", "google.nl", etc. as "Google".
		return "Google", true
	}
	if g, ok := groups[refURL.Host]; ok {
		return g, true
	}

	// Useful: https://lobste.rs/s/tslw6k/why_i_m_still_using_jquery_2019
	// Not really: https://lobste.rs/newest/page/8, https://lobste.rs/page/7
	//             https://lobste.rs/search, https://lobste.rs/t/javascript
	if refURL.Host == "lobste.rs" && !strings.HasPrefix(refURL.Path, "/s/") {
		return "lobste.rs", false
	}
	if refURL.Host == "gambe.ro" && !strings.HasPrefix(refURL.Path, "/s/") {
		return "lobste.rs", false
	}

	// Reddit
	// www.reddit.com/r/programming/top
	// www.reddit.com/r/programming/.compact
	// www.reddit.com/r/programming.compact
	// www.reddit.com/r/webdev/new
	// www.reddit.com/r/vim/search
	if refURL.Host == "www.reddit.com" {
		switch {
		case strings.HasSuffix(refURL.Path, "/top") || strings.HasSuffix(refURL.Path, "/new"):
			refURL.Path = refURL.Path[:len(refURL.Path)-4]
		case strings.HasSuffix(refURL.Path, "/search"):
			refURL.Path = refURL.Path[:len(refURL.Path)-7]
		case strings.HasSuffix(refURL.Path, ".compact"):
			refURL.Path = refURL.Path[:len(refURL.Path)-8]
		}
	}

	// Linking https://t.co/c3MITw38Yq isn't too useful as that will link back
	// to the page, so link to the Tweet instead.
	if refURL.Host == "t.co" {
		return "twitter.com/search?q=https%3A%2F%2Ft.co" +
			url.QueryEscape(refURL.Path), false
	}

	// Clean query parameters.
	i := strings.Index(ref, "?")
	if i == -1 {
		// No parameters so no work.
		return strings.TrimLeft(refURL.String(), "/"), false
	}

	q := refURL.Query()
	refURL.RawQuery = ""

	// Google analytics tracking parameters.
	q.Del("utm_source")
	q.Del("utm_medium")
	q.Del("utm_campaign")
	q.Del("utm_term")

	if len(q) == 0 {
		return refURL.String()[2:], false
	}
	return refURL.String()[2:], false
}

// ListRefsByPath lists all references for a path.
func (h *Stats) ListRefsByPath(ctx context.Context, path string, start, end time.Time, offset int) error {
	site := MustGetSite(ctx)

	limit := site.Settings.Limits.Ref
	if limit == 0 {
		limit = 10
	}

	err := zdb.MustGet(ctx).SelectContext(ctx, &h.Stats, `/* Stats.ListRefsByPath */
		select
			coalesce(sum(total), 0) as count,
			coalesce(sum(total_unique), 0) as count_unique,
			max(ref_scheme) as ref_scheme,
			ref as name
		from ref_counts
		where
			site=$1 and
			lower(path)=lower($2) and
			hour>=$3 and
			hour<=$4
		group by ref
		order by count_unique desc, ref desc
		limit $5 offset $6`,
		site.ID, path, start.Format(zdb.Date), end.Format(zdb.Date), limit+1, offset)

	if len(h.Stats) > limit {
		h.More = true
		h.Stats = h.Stats[:len(h.Stats)-1]
	}

	return errors.Wrap(err, "Stats.ListRefsByPath")
}

// ListTopRefs lists all ref statistics for the given time period, excluding
// referrals from the configured LinkDomain.
//
// The returned count is the count without LinkDomain, and is different from the
// total number of hits.
func (h *Stats) ListTopRefs(ctx context.Context, start, end time.Time, offset int) error {
	site := MustGetSite(ctx)

	limit := site.Settings.Limits.Hchart
	if limit == 0 {
		limit = 6
	}

	where := ` where site=? and hour>=? and hour<=?`
	args := []interface{}{site.ID, start.Format(zdb.Date), end.Format(zdb.Date)}
	if site.LinkDomain != "" {
		where += " and ref not like ? "
		args = append(args, site.LinkDomain+"%")
	}

	db := zdb.MustGet(ctx)
	err := db.SelectContext(ctx, &h.Stats, db.Rebind(`/* Stats.ListTopRefs */
		select
			coalesce(sum(total), 0) as count,
			coalesce(sum(total_unique), 0) as count_unique,
			max(ref_scheme) as ref_scheme,
			ref as name
		from ref_counts`+
		where+`
		group by ref
		order by count_unique desc
		limit ? offset ?`), append(args, limit+1, offset)...)
	if err != nil {
		return errors.Wrap(err, "Stats.ListAllRefs")
	}

	if len(h.Stats) > limit {
		h.More = true
		h.Stats = h.Stats[:len(h.Stats)-1]
	}

	return nil
}
