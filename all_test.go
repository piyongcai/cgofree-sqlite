// Copyright 2017 The Sqlite Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sqlite // import "modernc.org/sqlite"

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/mathutil"
)

func caller(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(2)
	fmt.Fprintf(os.Stderr, "# caller: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	_, fn, fl, _ = runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "# \tcallee: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func dbg(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "# dbg %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func TODO(...interface{}) string { //TODOOK
	_, fn, fl, _ := runtime.Caller(1)
	return fmt.Sprintf("# TODO: %s:%d:\n", path.Base(fn), fl) //TODOOK
}

func use(...interface{}) {}

func init() {
	use(caller, dbg, TODO) //TODOOK
}

// ============================================================================

var (
	recsPerSec = flag.Bool("recs_per_sec_as_mbps", false, "Show records per second as MB/s.")
)

func tempDB(t testing.TB) (string, *sql.DB) {
	dir, err := ioutil.TempDir("", "sqlite-test-")
	if err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open(driverName, filepath.Join(dir, "tmp.db"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}

	return dir, db
}

func TestScalar(t *testing.T) {
	dir, db := tempDB(t)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	t1 := time.Date(2017, 4, 20, 1, 2, 3, 56789, time.UTC)
	t2 := time.Date(2018, 5, 21, 2, 3, 4, 98765, time.UTC)
	r, err := db.Exec(`
	create table t(i int, f double, b bool, s text, t time);
	insert into t values(12, 3.14, ?, 'foo', ?), (34, 2.78, ?, 'bar', ?);
	`,
		true, t1,
		false, t2,
	)
	if err != nil {
		t.Fatal(err)
	}

	n, err := r.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}

	if g, e := n, int64(2); g != e {
		t.Fatal(g, e)
	}

	rows, err := db.Query("select * from t")
	if err != nil {
		t.Fatal(err)
	}

	type rec struct {
		i int
		f float64
		b bool
		s string
		t string
	}
	var a []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.i, &r.f, &r.b, &r.s, &r.t); err != nil {
			t.Fatal(err)
		}

		a = append(a, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if g, e := len(a), 2; g != e {
		t.Fatal(g, e)
	}

	if g, e := a[0], (rec{12, 3.14, true, "foo", t1.String()}); g != e {
		t.Fatal(g, e)
	}

	if g, e := a[1], (rec{34, 2.78, false, "bar", t2.String()}); g != e {
		t.Fatal(g, e)
	}
}

func TestBlob(t *testing.T) {
	dir, db := tempDB(t)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	b1 := []byte(time.Now().String())
	b2 := []byte("\x00foo\x00bar\x00")
	if _, err := db.Exec(`
	create table t(b blob);
	insert into t values(?), (?);
	`, b1, b2,
	); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query("select * from t")
	if err != nil {
		t.Fatal(err)
	}

	type rec struct {
		b []byte
	}
	var a []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.b); err != nil {
			t.Fatal(err)
		}

		a = append(a, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if g, e := len(a), 2; g != e {
		t.Fatal(g, e)
	}

	if g, e := a[0].b, b1; !bytes.Equal(g, e) {
		t.Fatal(g, e)
	}

	if g, e := a[1].b, b2; !bytes.Equal(g, e) {
		t.Fatal(g, e)
	}
}

func BenchmarkInsertMemory(b *testing.B) {
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		b.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	if _, err := db.Exec(`
	create table t(i int);
	begin;
	`); err != nil {
		b.Fatal(err)
	}

	s, err := db.Prepare("insert into t values(?)")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Exec(int64(i)); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if *recsPerSec {
		b.SetBytes(1e6)
	}
	if _, err := db.Exec(`commit;`); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkNextMemory(b *testing.B) {
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		b.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	if _, err := db.Exec(`
	create table t(i int);
	begin;
	`); err != nil {
		b.Fatal(err)
	}

	s, err := db.Prepare("insert into t values(?)")
	if err != nil {
		b.Fatal(err)
	}

	defer s.Close()

	for i := 0; i < b.N; i++ {
		if _, err := s.Exec(int64(i)); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := db.Exec("commit"); err != nil {
		b.Fatal(err)
	}

	r, err := db.Query("select * from t")
	if err != nil {

	}

	defer r.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !r.Next() {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if *recsPerSec {
		b.SetBytes(1e6)
	}
}

// https://gitlab.com/cznic/sqlite/issues/11
func TestIssue11(t *testing.T) {
	const N = 6570
	dir, db := tempDB(t)

	defer func() {
		db.Close()
		os.RemoveAll(dir)
	}()

	if _, err := db.Exec(`
	CREATE TABLE t1 (t INT);
	BEGIN;
`,
	); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < N; i++ {
		if _, err := db.Exec("INSERT INTO t1 (t) VALUES (?)", i); err != nil {
			t.Fatalf("#%v: %v", i, err)
		}
	}
	if _, err := db.Exec("COMMIT;"); err != nil {
		t.Fatal(err)
	}
}

// https://gitlab.com/cznic/sqlite/issues/12
func TestMemDB(t *testing.T) {
	// Verify we can create out-of-the heap memory DB instance.
	db, err := sql.Open(driverName, "file::memory:")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	v := strings.Repeat("a", 1024)
	if _, err := db.Exec(`
	create table t(s string);
	begin;
	`); err != nil {
		t.Fatal(err)
	}

	s, err := db.Prepare("insert into t values(?)")
	if err != nil {
		t.Fatal(err)
	}

	// Heap used to be fixed at 32MB.
	for i := 0; i < (64<<20)/len(v); i++ {
		if _, err := s.Exec(v); err != nil {
			t.Fatalf("%v * %v= %v: %v", i, len(v), i*len(v), err)
		}
	}
	if _, err := db.Exec(`commit;`); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	const (
		ngoroutines = 8
		nrows       = 5000
	)

	dir, err := ioutil.TempDir("", "sqlite-test-")
	if err != nil {
		t.Fatal(err)
	}

	defer os.RemoveAll(dir)

	db, err := sql.Open(driverName, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec("create table t(i)"); err != nil {
		t.Fatal(err)
	}

	prep, err := tx.Prepare("insert into t values(?)")
	if err != nil {
		t.Fatal(err)
	}

	rnd := make(chan int, 100)
	go func() {
		lim := ngoroutines * nrows
		rng, err := mathutil.NewFC32(0, lim-1, false)
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < lim; i++ {
			rnd <- int(rng.Next())
		}
	}()

	start := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < ngoroutines; i++ {
		wg.Add(1)

		go func(id int) {

			defer wg.Done()

		next:
			for i := 0; i < nrows; i++ {
				n := <-rnd
				var err error
				for j := 0; j < 10; j++ {
					if _, err := prep.Exec(n); err == nil {
						continue next
					}
				}

				t.Errorf("id %d, seq %d: %v", id, i, err)
				return
			}
		}(i)
	}
	t0 := time.Now()
	close(start)
	wg.Wait()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	d := time.Since(t0)
	rows, err := db.Query("select * from t order by i")
	if err != nil {
		t.Fatal(err)
	}

	var i int
	for ; rows.Next(); i++ {
		var j int
		if err := rows.Scan(&j); err != nil {
			t.Fatalf("seq %d: %v", i, err)
		}

		if g, e := j, i; g != e {
			t.Fatalf("seq %d: got %d, exp %d", i, g, e)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if g, e := i, ngoroutines*nrows; g != e {
		t.Fatalf("got %d rows, expected %d", g, e)
	}

	t.Logf("%d goroutines concurrently inserted %d rows in %v", ngoroutines, ngoroutines*nrows, d)
}

func TestConcurrentProcesses(t *testing.T) {
	dir, err := ioutil.TempDir("", "sqlite-test-")
	if err != nil {
		t.Fatal(err)
	}

	defer os.RemoveAll(dir)

	m, err := filepath.Glob(filepath.FromSlash("internal/mptest/*"))
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range m {
		if s := filepath.Ext(v); s != ".test" && s != ".subtest" {
			continue
		}

		b, err := ioutil.ReadFile(v)
		if err != nil {
			t.Fatal(err)
		}

		if err := ioutil.WriteFile(filepath.Join(dir, filepath.Base(v)), b, 0666); err != nil {
			t.Fatal(err)
		}
	}

	out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "mptest"), "modernc.org/sqlite/internal/mptest").CombinedOutput()
	if err != nil {
		t.Fatalf("%s\n%v", out, err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	defer os.Chdir(wd)

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

outer:
	for _, script := range m {
		script = filepath.Base(script)
		if filepath.Ext(script) != ".test" {
			continue
		}

		out, err := exec.Command(filepath.FromSlash("./mptest"), "db", "--trace", "2", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s\n%v", out, err)
		}

		a := strings.Split(string(out), "\n")
		for _, v := range a {
			if strings.HasPrefix(v, "Summary:") {
				b := strings.Fields(v)
				if len(b) < 2 {
					t.Fatalf("unexpected format of %q", v)
				}

				n, err := strconv.Atoi(b[1])
				if err != nil {
					t.Fatalf("unexpected format of %q", v)
				}

				if n != 0 {
					t.Errorf("%s", out)
				}

				t.Logf("%v: %v", script, v)
				continue outer
			}

		}
		t.Fatalf("%s\nerror: summary line not found", out)
	}
}

// https://gitlab.com/cznic/sqlite/issues/19
func TestIssue19(t *testing.T) {
	const (
		drop = `
drop table if exists products;
`

		up = `
CREATE TABLE IF NOT EXISTS "products" (
	"id"	VARCHAR(255),
	"user_id"	VARCHAR(255),
	"name"	VARCHAR(255),
	"description"	VARCHAR(255),
	"created_at"	BIGINT,
	"credits_price"	BIGINT,
	"enabled"	BOOLEAN,
	PRIMARY KEY("id")
);
`

		productInsert = `
INSERT INTO "products" ("id", "user_id", "name", "description", "created_at", "credits_price", "enabled") VALUES ('9be4398c-d527-4efb-93a4-fc532cbaf804', '16935690-348b-41a6-bb20-f8bb16011015', 'dqdwqdwqdwqwqdwqd', 'qwdwqwqdwqdwqdwqd', '1577448686', '1', '0');
INSERT INTO "products" ("id", "user_id", "name", "description", "created_at", "credits_price", "enabled") VALUES ('759f10bd-9e1d-4ec7-b764-0868758d7b85', '16935690-348b-41a6-bb20-f8bb16011015', 'qdqwqwdwqdwqdwqwqd', 'wqdwqdwqdwqdwqdwq', '1577448692', '1', '1');
INSERT INTO "products" ("id", "user_id", "name", "description", "created_at", "credits_price", "enabled") VALUES ('512956e7-224d-4b2a-9153-b83a52c4aa38', '16935690-348b-41a6-bb20-f8bb16011015', 'qwdwqwdqwdqdwqwqd', 'wqdwdqwqdwqdwqdwqdwqdqw', '1577448699', '2', '1');
INSERT INTO "products" ("id", "user_id", "name", "description", "created_at", "credits_price", "enabled") VALUES ('02cd138f-6fa6-4909-9db7-a9d0eca4a7b7', '16935690-348b-41a6-bb20-f8bb16011015', 'qdwqdwqdwqwqdwdq', 'wqddwqwqdwqdwdqwdqwq', '1577448706', '3', '1');
`
	)

	dir, err := ioutil.TempDir("", "sqlite-test-")
	if err != nil {
		t.Fatal(err)
	}

	defer os.RemoveAll(dir)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	defer os.Chdir(wd)

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "test.db")
	if err != nil {
		t.Fatal("failed to connect database")
	}

	defer db.Close()

	db.SetMaxOpenConns(1)

	if _, err = db.Exec(drop); err != nil {
		t.Fatal(err)
	}

	if _, err = db.Exec(up); err != nil {
		t.Fatal(err)
	}

	if _, err = db.Exec(productInsert); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err = db.QueryRow("select count(*) from products where user_id = ?", "16935690-348b-41a6-bb20-f8bb16011015").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 4 {
		t.Fatalf("expected result for the count query %d, we received %d\n", 4, count)
	}

	rows, err := db.Query("select * from products where user_id = ?", "16935690-348b-41a6-bb20-f8bb16011015")
	if err != nil {
		t.Fatal(err)
	}

	count = 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if count != 4 {
		t.Fatalf("expected result for the select query %d, we received %d\n", 4, count)
	}

	rows, err = db.Query("select * from products where enabled = ?", true)
	if err != nil {
		t.Fatal(err)
	}

	count = 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if count != 3 {
		t.Fatalf("expected result for the enabled select query %d, we received %d\n", 3, count)
	}
}
