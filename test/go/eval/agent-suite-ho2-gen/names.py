"""Similar-name traps where the named thing exists next to look-alike decoys."""
import json

from common import Task, d, cmd, go_filler, py_filler, GO_MOD, UNITTEST, sub


# ------------------------------------------------------------ Go: rename one Evict among many
def go_rename_remove():
    t = Task("go-rename-remove")
    t.fx("go.mod", GO_MOD.format(mod="slab"))
    lru = d('''
        // Package lru is a fixed-capacity least-recently-used byte store.
        package lru

        import "container/list"

        type entry struct {
            key string
            val []byte
        }

        // Store keeps at most Cap entries and drops the least recently used one
        // when a Put would exceed it.
        type Store struct {
            Cap int
            // OnEvict, when set, is called with the key of every entry that leaves
            // the store, whatever the reason.
            OnEvict func(key string)

            ll    *list.List
            items map[string]*list.Element
        }

        // New returns an empty store holding at most capacity entries.
        func New(capacity int) *Store {
            return &Store{Cap: capacity, ll: list.New(), items: map[string]*list.Element{}}
        }

        // Get returns the value for key and marks it as recently used.
        func (s *Store) Get(key string) ([]byte, bool) {
            e, ok := s.items[key]
            if !ok {
                return nil, false
            }
            s.ll.MoveToFront(e)
            return e.Value.(*entry).val, true
        }

        // Put inserts or replaces key.
        func (s *Store) Put(key string, val []byte) {
            if e, ok := s.items[key]; ok {
                e.Value.(*entry).val = val
                s.ll.MoveToFront(e)
                return
            }
            s.items[key] = s.ll.PushFront(&entry{key: key, val: val})
            for s.ll.Len() > s.Cap {
                s.evictOldest()
            }
        }

        // Evict removes key from the store. It does nothing when key is absent.
        func (s *Store) Evict(key string) {
            if e, ok := s.items[key]; ok {
                s.evict(e)
            }
        }

        // EvictAll empties the store, oldest entries first.
        func (s *Store) EvictAll() {
            for s.ll.Len() > 0 {
                s.evictOldest()
            }
        }

        // Len reports the number of entries.
        func (s *Store) Len() int { return s.ll.Len() }

        func (s *Store) evictOldest() {
            if e := s.ll.Back(); e != nil {
                s.evict(e)
            }
        }

        func (s *Store) evict(e *list.Element) {
            ent := s.ll.Remove(e).(*entry)
            delete(s.items, ent.key)
            if s.OnEvict != nil {
                s.OnEvict(ent.key)
            }
        }
        ''')
    t.fx("lru/lru.go", lru)
    lru_test = d('''
        package lru

        import "testing"

        func TestPutGetEvict(t *testing.T) {
            var gone []string
            s := New(2)
            s.OnEvict = func(k string) { gone = append(gone, k) }
            s.Put("a", []byte("1"))
            s.Put("b", []byte("2"))
            s.Get("a")
            s.Put("c", []byte("3")) // drops b
            if _, ok := s.Get("b"); ok {
                t.Fatal("b should be gone")
            }
            s.Evict("a")
            s.Evict("missing")
            if s.Len() != 1 {
                t.Fatalf("Len = %d", s.Len())
            }
            s.EvictAll()
            if s.Len() != 0 || len(gone) != 3 {
                t.Fatalf("len=%d gone=%v", s.Len(), gone)
            }
        }
        ''')
    t.fx("lru/lru_test.go", lru_test)
    ttl = d('''
        // Package ttl is a map whose entries expire after a fixed time to live.
        package ttl

        import "time"

        // Cache holds string values until they expire.
        type Cache struct {
            TTL time.Duration
            Now func() time.Time

            vals map[string]item
        }

        type item struct {
            val     string
            expires time.Time
        }

        // NewCache returns an empty cache whose entries live for ttl.
        func NewCache(ttl time.Duration) *Cache {
            return &Cache{TTL: ttl, Now: time.Now, vals: map[string]item{}}
        }

        // Set stores val under key.
        func (c *Cache) Set(key, val string) {
            c.vals[key] = item{val: val, expires: c.Now().Add(c.TTL)}
        }

        // Get returns the value for key unless it has expired.
        func (c *Cache) Get(key string) (string, bool) {
            it, ok := c.vals[key]
            if !ok || !c.Now().Before(it.expires) {
                return "", false
            }
            return it.val, true
        }

        // Evict drops key immediately, expired or not.
        func (c *Cache) Evict(key string) {
            delete(c.vals, key)
        }

        // Sweep drops every expired entry and reports how many it dropped.
        func (c *Cache) Sweep() int {
            n := 0
            now := c.Now()
            for k, it := range c.vals {
                if !now.Before(it.expires) {
                    delete(c.vals, k)
                    n++
                }
            }
            return n
        }
        ''')
    t.fx("ttl/ttl.go", ttl)
    t.fx("ttl/ttl_test.go", d('''
        package ttl

        import (
            "testing"
            "time"
        )

        func TestExpiry(t *testing.T) {
            now := time.Unix(1000, 0)
            c := NewCache(time.Minute)
            c.Now = func() time.Time { return now }
            c.Set("k", "v")
            if v, ok := c.Get("k"); !ok || v != "v" {
                t.Fatal("want v")
            }
            c.Evict("k")
            if _, ok := c.Get("k"); ok {
                t.Fatal("evicted")
            }
            c.Set("x", "y")
            now = now.Add(2 * time.Minute)
            if n := c.Sweep(); n != 1 {
                t.Fatalf("Sweep = %d", n)
            }
        }
        '''))
    session = d('''
        // Package session keeps logged-in sessions and their CSRF tokens.
        package session

        import (
            "time"

            "slab/lru"
            "slab/ttl"
        )

        // Manager stores session payloads in an LRU (the "cache") and short-lived
        // CSRF tokens in an expiring map (the "store").
        type Manager struct {
            cache *lru.Store
            store *ttl.Cache
        }

        // NewManager keeps up to max sessions; CSRF tokens live for csrfTTL.
        func NewManager(max int, csrfTTL time.Duration) *Manager {
            return &Manager{cache: lru.New(max), store: ttl.NewCache(csrfTTL)}
        }

        // Login records a session and its CSRF token.
        func (m *Manager) Login(id string, payload []byte, csrf string) {
            m.cache.Put(id, payload)
            m.store.Set("csrf:"+id, csrf)
        }

        // Session returns the payload of a live session.
        func (m *Manager) Session(id string) ([]byte, bool) {
            return m.cache.Get(id)
        }

        // CSRF returns the CSRF token of a session.
        func (m *Manager) CSRF(id string) (string, bool) {
            return m.store.Get("csrf:" + id)
        }

        // Logout forgets the session and its CSRF token.
        func (m *Manager) Logout(id string) {
            m.cache.Evict(id)
            m.store.Evict("csrf:" + id)
        }

        // RotateCSRF replaces the CSRF token of a session.
        func (m *Manager) RotateCSRF(id, token string) {
            m.store.Evict("csrf:" + id)
            m.store.Set("csrf:"+id, token)
        }

        // Reset logs everybody out.
        func (m *Manager) Reset() {
            m.cache.EvictAll()
        }
        ''')
    t.fx("session/manager.go", session)
    t.fx("session/manager_test.go", d('''
        package session

        import (
            "testing"
            "time"
        )

        func TestLoginLogout(t *testing.T) {
            m := NewManager(4, time.Hour)
            m.Login("s1", []byte("alice"), "tok")
            if _, ok := m.Session("s1"); !ok {
                t.Fatal("no session")
            }
            m.Logout("s1")
            if _, ok := m.Session("s1"); ok {
                t.Fatal("still logged in")
            }
            if _, ok := m.CSRF("s1"); ok {
                t.Fatal("csrf left behind")
            }
        }
        '''))
    purge = d('''
        // Package admin holds maintenance operations used by slabctl.
        package admin

        import (
            "slab/lru"
            "slab/ttl"
        )

        // PurgeKeys removes keys from an LRU store and reports how many keys it
        // was asked to purge.
        func PurgeKeys(s *lru.Store, keys []string) int {
            for _, k := range keys {
                s.Evict(k)
            }
            return len(keys)
        }

        // PurgeTokens drops tokens from an expiring cache.
        func PurgeTokens(c *ttl.Cache, tokens []string) {
            for _, tok := range tokens {
                c.Evict(tok)
            }
        }

        // Flush empties an LRU store and returns how many entries it held.
        func Flush(s *lru.Store) int {
            n := s.Len()
            s.EvictAll()
            return n
        }
        ''')
    t.fx("admin/purge.go", purge)
    t.fx("admin/purge_test.go", d('''
        package admin

        import (
            "testing"
            "time"

            "slab/lru"
            "slab/ttl"
        )

        func TestPurge(t *testing.T) {
            s := lru.New(8)
            s.Put("a", nil)
            s.Put("b", nil)
            PurgeKeys(s, []string{"a", "b"})
            if s.Len() != 0 {
                t.Fatalf("Len = %d", s.Len())
            }
            c := ttl.NewCache(time.Hour)
            c.Set("t", "1")
            PurgeTokens(c, []string{"t"})
            if _, ok := c.Get("t"); ok {
                t.Fatal("token left")
            }
        }
        '''))
    main_go = d('''
        // Command slabctl is a tiny interactive front end for the stores.
        package main

        import (
            "bufio"
            "fmt"
            "os"
            "strings"
            "time"

            "slab/admin"
            "slab/lru"
            "slab/ttl"
        )

        func main() {
            cache := lru.New(128)
            store := ttl.NewCache(10 * time.Minute)
            in := bufio.NewScanner(os.Stdin)
            for in.Scan() {
                f := strings.Fields(in.Text())
                if len(f) == 0 {
                    continue
                }
                switch f[0] {
                case "put":
                    cache.Put(f[1], []byte(strings.Join(f[2:], " ")))
                case "evict":
                    cache.Evict(f[1])
                    fmt.Println("ok")
                case "token":
                    store.Set(f[1], f[2])
                case "revoke":
                    store.Evict(f[1])
                case "purge":
                    fmt.Println(admin.PurgeKeys(cache, f[1:]), "purged")
                case "flush":
                    fmt.Println(admin.Flush(cache), "flushed")
                }
            }
        }
        ''')
    t.fx("cmd/slabctl/main.go", main_go)
    api = d('''
        # slab API

        ## lru.Store

        - `New(capacity int) *Store`
        - `Store.Get(key string) ([]byte, bool)`
        - `Store.Put(key string, val []byte)`
        - `Store.Evict(key string)` - removes key; no-op when absent.
        - `Store.EvictAll()` - empties the store.
        - `Store.OnEvict func(key string)` - hook called for every entry that leaves the store.

        ## ttl.Cache

        - `NewCache(ttl time.Duration) *Cache`
        - `Cache.Set(key, val string)` / `Cache.Get(key string) (string, bool)`
        - `Cache.Evict(key string)` - drops key immediately.
        - `Cache.Sweep() int` - drops expired entries.

        ## admin

        - `PurgeKeys(s *lru.Store, keys []string) int` - returns how many keys it was asked to purge.
        - `PurgeTokens(c *ttl.Cache, tokens []string)`
        - `Flush(s *lru.Store) int`
        ''')
    t.fx("docs/API.md", api)
    t.fx("README.md", "# slab\n\nIn-memory stores (LRU and TTL) plus the slabctl tool. See docs/API.md.\n")
    for p in ["codec", "stats"]:
        go_filler(t, p, count=3)

    t.ref("lru/lru.go", sub(lru, (
        '''// Evict removes key from the store. It does nothing when key is absent.
        func (s *Store) Evict(key string) {
            if e, ok := s.items[key]; ok {
                s.evict(e)
            }
        }'''.replace("\n        ", "\n"),
        '''// Remove removes key from the store and reports whether it was present.
        func (s *Store) Remove(key string) bool {
            e, ok := s.items[key]
            if ok {
                s.evict(e)
            }
            return ok
        }'''.replace("\n        ", "\n"))))
    t.ref("lru/lru_test.go", sub(lru_test, ('    s.Evict("a")\n    s.Evict("missing")',
                                             '    if !s.Remove("a") {\n        t.Fatal("a was present")\n    }\n'
                                             '    if s.Remove("missing") {\n        t.Fatal("missing was absent")\n    }')))
    t.ref("session/manager.go", sub(session, ("m.cache.Evict(id)", "m.cache.Remove(id)")))
    t.ref("admin/purge.go", sub(purge, (
        '''// PurgeKeys removes keys from an LRU store and reports how many keys it
        // was asked to purge.
        func PurgeKeys(s *lru.Store, keys []string) int {
            for _, k := range keys {
                s.Evict(k)
            }
            return len(keys)
        }'''.replace("\n        ", "\n"),
        '''// PurgeKeys removes keys from an LRU store and reports how many of them
        // were actually present.
        func PurgeKeys(s *lru.Store, keys []string) int {
            n := 0
            for _, k := range keys {
                if s.Remove(k) {
                    n++
                }
            }
            return n
        }'''.replace("\n        ", "\n"))))
    t.ref("cmd/slabctl/main.go", sub(main_go, ("cache.Evict(f[1])", "cache.Remove(f[1])")))
    t.ref("docs/API.md", sub(api, ("- `Store.Evict(key string)` - removes key; no-op when absent.",
                                   "- `Store.Remove(key string) bool` - removes key; reports whether it was present."),
                             ("returns how many keys it was asked to purge.", "returns how many of the keys were present.")))
    t.answer("Renamed lru.Store.Evict to Remove (returns whether the key was present) and updated its callers in "
             "session/manager.go, admin/purge.go and cmd/slabctl, the lru test and docs/API.md. PurgeKeys now counts "
             "only keys that were actually removed. ttl.Cache.Evict, EvictAll and OnEvict are unchanged.\n")
    t.gr("lru/zz_hidden_test.go", d('''
        package lru

        import (
            "reflect"
            "testing"
        )

        func TestHiddenRemove(t *testing.T) {
            var gone []string
            s := New(3)
            s.OnEvict = func(k string) { gone = append(gone, k) }
            s.Put("a", nil)
            s.Put("b", nil)
            var ok bool = s.Remove("a")
            if !ok || s.Remove("a") || s.Remove("zzz") {
                t.Fatal("Remove must report presence")
            }
            if len(gone) != 1 || gone[0] != "a" {
                t.Fatalf("OnEvict calls = %v", gone)
            }
            s.EvictAll()
            if s.Len() != 0 {
                t.Fatal("EvictAll")
            }
            typ := reflect.TypeOf(s)
            if _, has := typ.MethodByName("Evict"); has {
                t.Fatal("Store.Evict should be renamed to Remove")
            }
            if _, has := typ.MethodByName("RemoveAll"); has {
                t.Fatal("EvictAll must keep its name")
            }
        }
        '''))
    t.gr("ttl/zz_hidden_test.go", d('''
        package ttl

        import (
            "reflect"
            "testing"
            "time"
        )

        func TestHiddenCacheUnchanged(t *testing.T) {
            c := NewCache(time.Hour)
            c.Set("k", "v")
            c.Evict("k")
            if _, ok := c.Get("k"); ok {
                t.Fatal("Evict")
            }
            if _, has := reflect.TypeOf(c).MethodByName("Remove"); has {
                t.Fatal("ttl.Cache must not get a Remove method")
            }
        }
        '''))
    t.gr("admin/zz_hidden_test.go", d('''
        package admin

        import (
            "testing"

            "slab/lru"
        )

        func TestHiddenPurgeCount(t *testing.T) {
            s := lru.New(8)
            s.Put("a", nil)
            s.Put("b", nil)
            s.Put("c", nil)
            if n := PurgeKeys(s, []string{"a", "x", "c", "a"}); n != 2 {
                t.Fatalf("PurgeKeys = %d, want 2", n)
            }
            if s.Len() != 1 {
                t.Fatalf("Len = %d", s.Len())
            }
            if n := PurgeKeys(s, nil); n != 0 {
                t.Fatal(n)
            }
        }
        '''))
    t.gr("session/zz_hidden_test.go", d('''
        package session

        import (
            "testing"
            "time"
        )

        func TestHiddenRotateAndLogout(t *testing.T) {
            m := NewManager(4, time.Hour)
            m.Login("s1", []byte("a"), "t1")
            m.RotateCSRF("s1", "t2")
            if tok, ok := m.CSRF("s1"); !ok || tok != "t2" {
                t.Fatalf("CSRF = %q %v", tok, ok)
            }
            m.Login("s2", []byte("b"), "u")
            m.Logout("s1")
            if _, ok := m.Session("s2"); !ok {
                t.Fatal("s2 lost")
            }
            if _, ok := m.CSRF("s1"); ok {
                t.Fatal("csrf left")
            }
        }
        '''))
    t.register(
        name="Rename one Evict method among same-named and similar-named decoys (misleading field names)",
        category="similar-name", tags=["go", "similar-name", "rename", "multi-file", "decoys"], difficulty="hard",
        prompt=("In this Go module, rename the method `Evict` of `lru.Store` to `Remove` and make it return a `bool` "
                "that reports whether the key was present. Only that one method is renamed - every other "
                "identifier keeps its name and behaviour. Update all of its callers, the tests and docs/API.md. "
                "Also make `admin.PurgeKeys` return how many of the given keys were actually removed (instead of "
                "len(keys)) and update its doc comment. `go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["lru/**", "session/*.go", "admin/*.go", "cmd/slabctl/main.go", "docs/API.md"],
        forbiddenChangedFiles=["ttl/**", "codec/**", "stats/**", "go.mod"],
        expectedChangedFiles=["lru/lru.go", "session/manager.go", "admin/purge.go", "cmd/slabctl/main.go", "docs/API.md"],
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[
            {"id": "remove-signature", "path": "lru/lru.go",
             "regex": [r"func \(\w+ \*Store\) Remove\(\w+ string\) bool", r"func \(\w+ \*Store\) EvictAll\(\)"],
             "notRegex": [r"func \(\w+ \*Store\) Evict\("], "critical": True},
            {"id": "ttl-calls-kept", "path": "session/manager.go",
             "regex": [r"m\.store\.Evict\(\"csrf:\" \+ id\)", r"m\.cache\.EvictAll\(\)"],
             "notRegex": [r"m\.store\.Remove\(", r"m\.cache\.Evict\("], "critical": True},
            {"id": "ttl-calls-kept-admin", "path": "admin/purge.go", "regex": [r"c\.Evict\(tok\)", r"s\.EvictAll\(\)"],
             "critical": True},
            {"id": "cli-revoke-kept", "path": "cmd/slabctl/main.go", "regex": [r"store\.Evict\(f\[1\]\)"],
             "notRegex": [r"cache\.Evict\(", r"store\.Remove\("], "critical": True},
            {"id": "docs", "path": "docs/API.md", "regex": [r"Remove\(", r"Cache\.Evict\(key string\)", r"Store\.EvictAll\(\)"],
             "notRegex": [r"Store\.Evict\("]},
        ],
    )


# ------------------------------------------------------------ Python: change slugify, not its siblings
def _run_original(src, calls):
    ns = {}
    exec(compile(src, "text.py", "exec"), ns)
    return [ns[fn](*args) for fn, args in calls]


def py_slugify():
    t = Task("py-slugify-decoys")
    text_py = d('''
        """Text helpers shared by the blog engine."""

        import re
        import unicodedata


        def _ascii_fold(s):
            """Strip accents: NFKD-decompose and drop everything that is not ASCII."""
            return unicodedata.normalize("NFKD", s).encode("ascii", "ignore").decode("ascii")


        def slugify(title, max_len=60):
            """URL slug for a post title: "Hello, World!" -> "hello-world"."""
            s = _ascii_fold(title).lower()
            s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
            return s[:max_len].rstrip("-")


        def slugify_filename(name):
            """Safe attachment file name: keeps the extension, uses underscores."""
            base, dot, ext = name.rpartition(".")
            if not dot:
                base, ext = name, ""
            base = re.sub(r"[^A-Za-z0-9]+", "_", _ascii_fold(base)).strip("_") or "file"
            return base + ("." + _ascii_fold(ext).lower() if ext else "")


        def _slugify_legacy(title):
            """Permalink slug of posts published before 2024.

            Old URLs are linked from all over the web, so this must keep producing
            exactly the same output.
            """
            s = _ascii_fold(title).lower()
            return re.sub(r"\\s+", "-", s.strip())


        def excerpt(body, words=30):
            """First `words` words of body, with an ellipsis when cut."""
            parts = body.split()
            return " ".join(parts[:words]) + (" ..." if len(parts) > words else "")
        ''')
    t.fx("blogkit/text.py", text_py)
    t.fx("blogkit/__init__.py", d('''
        """blogkit: a tiny static blog engine."""

        from blogkit.text import excerpt, slugify, slugify_filename

        __all__ = ["excerpt", "slugify", "slugify_filename"]
        '''))
    posts = d('''
        """Post model and permalinks."""

        import datetime
        from dataclasses import dataclass

        from blogkit.text import _slugify_legacy, slugify

        LEGACY_CUTOFF = datetime.date(2024, 1, 1)


        @dataclass
        class Post:
            title: str
            published: datetime.date
            body: str = ""

            @property
            def slug(self):
                if self.published < LEGACY_CUTOFF:
                    return _slugify_legacy(self.title)
                return slugify(self.title)

            def permalink(self):
                return "/%04d/%02d/%s/" % (self.published.year, self.published.month, self.slug)
        ''')
    t.fx("blogkit/posts.py", posts)
    attach = d('''
        """Attachment storage paths."""

        import os

        from blogkit.text import slugify_filename


        def attachment_path(post_slug, filename):
            return os.path.join("media", post_slug, slugify_filename(filename)).replace(os.sep, "/")
        ''')
    t.fx("blogkit/attachments.py", attach)
    compat = d('''
        """blogkit 1.x API, kept for old plugins. Do not change behaviour."""

        import re
        import unicodedata


        def slugify(title):
            s = unicodedata.normalize("NFKD", title).encode("ascii", "ignore").decode("ascii")
            return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")
        ''')
    t.fx("blogkit/compat/__init__.py", "")
    t.fx("blogkit/compat/v1.py", compat)
    t.fx("tests/__init__.py", "")
    t.fx("tests/test_text.py", d('''
        import datetime
        import unittest

        from blogkit import slugify, slugify_filename
        from blogkit.attachments import attachment_path
        from blogkit.posts import Post


        class TextTest(unittest.TestCase):
            def test_slugify(self):
                self.assertEqual(slugify("Hello, World!"), "hello-world")
                self.assertEqual(slugify("Crème brûlée"), "creme-brulee")
                self.assertEqual(slugify("a" * 80), "a" * 60)

            def test_filename(self):
                self.assertEqual(slugify_filename("My Photo (1).JPG"), "My_Photo_1.jpg")
                self.assertEqual(attachment_path("p", "x y.png"), "media/p/x_y.png")

            def test_permalinks(self):
                old = Post("Hello  World", datetime.date(2023, 5, 1))
                new = Post("Hello  World", datetime.date(2024, 5, 1))
                self.assertEqual(old.permalink(), "/2023/05/hello-world/")
                self.assertEqual(new.permalink(), "/2024/05/hello-world/")


        if __name__ == "__main__":
            unittest.main()
        '''))
    t.fx("README.md", "# blogkit\n\nA tiny static blog engine. Tests: `python -m unittest discover -s tests -t .`.\n")
    py_filler(t, "blogkit/themes", count=4)
    py_filler(t, "blogkit/feeds", count=3)

    # Expected outputs of the helpers that must not change, computed from the original code.
    keep_calls = [("slugify_filename", ("Łódź nocą.JPG",)), ("slugify_filename", ("Żółć.txt",)),
                  ("slugify_filename", ("ŁŁ",)), ("_slugify_legacy", ("Łódź nocą",)),
                  ("_slugify_legacy", ("Zażółć Gęślą Jaźń",))]
    kept = _run_original(text_py, keep_calls)
    compat_ns = {}
    exec(compile(compat, "v1.py", "exec"), compat_ns)
    compat_out = compat_ns["slugify"]("Łódź nocą")

    t.ref("blogkit/text.py", sub(text_py, (
        '''def slugify(title, max_len=60):
            """URL slug for a post title: "Hello, World!" -> "hello-world"."""
            s = _ascii_fold(title).lower()'''.replace("\n        ", "\n"),
        '''# Letters that NFKD does not decompose into an ASCII base letter.
        _TRANSLIT = str.maketrans({"ł": "l", "Ł": "L"})


        def slugify(title, max_len=60):
            """URL slug for a post title: "Hello, World!" -> "hello-world", "Łódź" -> "lodz"."""
            s = _ascii_fold(title.translate(_TRANSLIT)).lower()'''.replace("\n        ", "\n"))))
    t.answer("slugify now transliterates Ł/ł to l before folding accents, so \"Łódź nocą\" becomes \"lodz-noca\". The "
             "shared _ascii_fold helper is unchanged, so slugify_filename, the legacy permalink slugs and "
             "blogkit.compat.v1.slugify produce exactly what they did before.\n")
    hidden = d('''
        import datetime
        import unittest

        import blogkit
        from blogkit import text
        from blogkit.compat import v1
        from blogkit.posts import Post

        KEEP = %(keep)s


        class HiddenTest(unittest.TestCase):
            def test_slugify_polish(self):
                cases = {
                    "Łódź nocą": "lodz-noca",
                    "Żółta łąka": "zolta-laka",
                    "ZAŻÓŁĆ GĘŚLĄ JAŹŃ": "zazolc-gesla-jazn",
                    "Hello, World!": "hello-world",
                    "Crème brûlée": "creme-brulee",
                    "  --Ćma i ŁOŚ--  ": "cma-i-los",
                }
                for title, want in cases.items():
                    self.assertEqual(blogkit.slugify(title), want, title)
                self.assertEqual(blogkit.slugify("ł" * 80), "l" * 60)
                self.assertEqual(blogkit.slugify("Łódź", max_len=3), "lod")

            def test_new_permalink(self):
                self.assertEqual(Post("Łódź nocą", datetime.date(2025, 2, 1)).permalink(), "/2025/02/lodz-noca/")

            def test_siblings_unchanged(self):
                for (fn, args), want in KEEP:
                    self.assertEqual(getattr(text, fn)(*args), want, (fn, args))
                self.assertEqual(Post("Łódź nocą", datetime.date(2020, 2, 1)).slug, %(legacy)r)
                self.assertEqual(v1.slugify("Łódź nocą"), %(compat)r)


        if __name__ == "__main__":
            unittest.main()
        ''') % {"keep": json.dumps([[list(c), k] for c, k in zip(keep_calls, kept)], ensure_ascii=False),
                "legacy": kept[3], "compat": compat_out}
    t.gr("tests/test_zz_hidden.py", hidden)
    t.register(
        name="Change one slug function whose siblings share its helper and must keep their output",
        category="similar-name", tags=["python", "similar-name", "decoys", "unicode"], difficulty="medium",
        prompt=("`blogkit.slugify` mangles Polish titles: \"Łódź nocą\" becomes \"odz-noca\" but it should be "
                "\"lodz-noca\" (ł/Ł must become l, the other Polish letters already lose their accents correctly). "
                "Fix `blogkit.slugify` only. Everything else that turns text into slugs or names - attachment file "
                "names, the permalinks of pre-2024 posts and the 1.x compatibility API - must keep producing exactly "
                "the output it produces today. The tests must keep passing."),
        strictDiff=True,
        allowedChangedFiles=["blogkit/text.py", "tests/test_*.py"],
        forbiddenChangedFiles=["blogkit/compat/**", "blogkit/posts.py", "blogkit/attachments.py"],
        expectedChangedFiles=["blogkit/text.py"],
        verificationCommands=[cmd("unittest", *UNITTEST)],
    )


def build():
    go_rename_remove()
    py_slugify()
