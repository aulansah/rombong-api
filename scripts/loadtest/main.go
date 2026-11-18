// Load and concurrency tests against a running go-ecommerce-api.
// Standard library only. Writes results as JSON for the report.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var base = "http://localhost:8080/api/v1"

var client = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{MaxIdleConns: 1000, MaxIdleConnsPerHost: 1000, MaxConnsPerHost: 0},
}

type resp struct {
	Status int
	Body   map[string]any
}

func call(method, path, token string, body any, headers map[string]string) (resp, time.Duration, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return resp{}, time.Since(start), err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	d := time.Since(start)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp{res.StatusCode, m}, d, nil
}

func data(r resp) map[string]any { m, _ := r.Body["data"].(map[string]any); return m }

// mustCall performs a request and exits on a transport error or an
// unexpected status (want 0 = any 2xx).
func mustCall(want int, method, path, token string, body any, headers map[string]string) resp {
	r, _, err := call(method, path, token, body, headers)
	if err != nil || (want == 0 && (r.Status < 200 || r.Status > 299)) || (want != 0 && r.Status != want) {
		fmt.Fprintf(os.Stderr, "%s %s: status %d err %v body %v\n", method, path, r.Status, err, r.Body)
		os.Exit(1)
	}
	return r
}

// ---------------------------------------------------------------------------

type LoadResult struct {
	Name        string         `json:"name"`
	Endpoint    string         `json:"endpoint"`
	Concurrency int            `json:"concurrency"`
	DurationS   float64        `json:"duration_s"`
	Requests    int            `json:"requests"`
	RPS         float64        `json:"rps"`
	P50         float64        `json:"p50_ms"`
	P90         float64        `json:"p90_ms"`
	P95         float64        `json:"p95_ms"`
	P99         float64        `json:"p99_ms"`
	Max         float64        `json:"max_ms"`
	Mean        float64        `json:"mean_ms"`
	ErrorRate   float64        `json:"error_rate"`
	Statuses    map[string]int `json:"statuses"`
}

func pct(s []time.Duration, p float64) float64 {
	if len(s) == 0 {
		return 0
	}
	i := int(float64(len(s)-1) * p)
	return float64(s[i].Microseconds()) / 1000
}

// load runs fn from `conc` workers for `dur` and summarizes latencies.
func load(name, endpoint string, conc int, dur time.Duration, fn func(worker int) (int, time.Duration, error)) LoadResult {
	var mu sync.Mutex
	lat := make([]time.Duration, 0, 100000)
	statuses := map[string]int{}
	var errs int64
	deadline := time.Now().Add(dur)
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := make([]time.Duration, 0, 4096)
			localSt := map[string]int{}
			for time.Now().Before(deadline) {
				st, d, err := fn(w)
				local = append(local, d)
				key := fmt.Sprint(st)
				if err != nil {
					key = "error"
				}
				localSt[key]++
				if err != nil || st >= 500 {
					atomic.AddInt64(&errs, 1)
				}
			}
			mu.Lock()
			lat = append(lat, local...)
			for k, v := range localSt {
				statuses[k] += v
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	var sum time.Duration
	for _, d := range lat {
		sum += d
	}
	n := len(lat)
	r := LoadResult{
		Name: name, Endpoint: endpoint, Concurrency: conc, DurationS: elapsed, Requests: n,
		RPS: float64(n) / elapsed, P50: pct(lat, .50), P90: pct(lat, .90), P95: pct(lat, .95), P99: pct(lat, .99),
		Max: pct(lat, 1), Mean: float64(sum.Microseconds()) / 1000 / float64(max(n, 1)),
		ErrorRate: float64(errs) / float64(max(n, 1)), Statuses: statuses,
	}
	fmt.Printf("%-34s c=%-4d req=%-7d rps=%-8.0f p50=%-7.1f p95=%-7.1f p99=%-7.1f err=%.2f%% %v\n",
		name, conc, n, r.RPS, r.P50, r.P95, r.P99, r.ErrorRate*100, statuses)
	return r
}

// ---------------------------------------------------------------------------

type Report struct {
	Environment map[string]any   `json:"environment"`
	Load        []LoadResult     `json:"load"`
	Ramp        []LoadResult     `json:"ramp"`
	NoCache     []LoadResult     `json:"no_cache"`
	Concurrency []map[string]any `json:"concurrency"`
}

func register(i int) string {
	email := fmt.Sprintf("load%d-%d@example.com", i, time.Now().UnixNano())
	mustCall(0, "POST", "/auth/register", "", map[string]any{"name": fmt.Sprintf("Load %d", i), "email": email, "password": "Passw0rd123"}, nil)
	r := mustCall(0, "POST", "/auth/login", "", map[string]any{"email": email, "password": "Passw0rd123"}, nil)
	return data(r)["tokens"].(map[string]any)["access_token"].(string)
}

func parallel(n, conc int, fn func(i int)) {
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) { defer wg.Done(); fn(i); <-sem }(i)
	}
	wg.Wait()
}

func main() {
	rep := Report{Environment: map[string]any{}}
	out, _ := exec.Command("bash", "-c", "nproc; grep 'model name' /proc/cpuinfo | head -1 | cut -d: -f2; free -g | awk '/Mem/{print $2}'; go version; psql --version; redis-server --version | cut -d' ' -f3").Output()
	rep.Environment["raw"] = string(out)
	rep.Environment["started_at"] = time.Now().Format(time.RFC3339)

	// --- setup -------------------------------------------------------------
	adm := data(mustCall(200, "POST", "/auth/login", "", map[string]any{"email": "admin@example.com", "password": "Admin12345"}, nil))
	admin := adm["tokens"].(map[string]any)["access_token"].(string)
	cat := data(mustCall(0, "POST", "/categories", admin, map[string]any{"name": fmt.Sprintf("Load %d", time.Now().Unix())}, nil))["id"].(string)
	var ids []string
	for i := 0; i < 100; i++ {
		p := data(mustCall(0, "POST", "/products", admin, map[string]any{"name": fmt.Sprintf("Produk Uji %03d", i), "description": "load test", "price": 10000 + i*500, "stock": 1000, "category_id": cat}, nil))
		ids = append(ids, p["id"].(string))
	}
	fmt.Println("seeded 100 products")

	get := func(path string) func(int) (int, time.Duration, error) {
		return func(int) (int, time.Duration, error) {
			r, d, err := call("GET", path, "", nil, nil)
			return r.Status, d, err
		}
	}
	var ctr int64
	detail := func(int) (int, time.Duration, error) {
		id := ids[atomic.AddInt64(&ctr, 1)%int64(len(ids))]
		r, d, err := call("GET", "/products/"+id, "", nil, nil)
		return r.Status, d, err
	}

	// warm cache
	call("GET", "/products?page=1&limit=20", "", nil, nil)

	// --- steady load -------------------------------------------------------
	rep.Load = append(rep.Load,
		load("Katalog: list produk (cache)", "GET /products?page=1&limit=20", 50, 20*time.Second, get("/products?page=1&limit=20")),
		load("Katalog: detail produk (cache)", "GET /products/:id", 50, 20*time.Second, detail),
		load("Katalog: pencarian", "GET /products?search=Uji 0", 50, 15*time.Second, get("/products?search=Uji%200&limit=20")),
		load("Health check", "GET /health", 50, 10*time.Second, func(int) (int, time.Duration, error) {
			req, _ := http.NewRequest("GET", "http://localhost:8080/health", nil)
			s := time.Now()
			res, err := client.Do(req)
			if err != nil {
				return 0, time.Since(s), err
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			return res.StatusCode, time.Since(s), nil
		}),
	)

	// login (bcrypt cost 12, CPU bound by design)
	email := fmt.Sprintf("loginload-%d@example.com", time.Now().UnixNano())
	mustCall(201, "POST", "/auth/register", "", map[string]any{"name": "Login Load", "email": email, "password": "Passw0rd123"}, nil)
	rep.Load = append(rep.Load, load("Auth: login (bcrypt cost 12)", "POST /auth/login", 16, 15*time.Second, func(int) (int, time.Duration, error) {
		r, d, err := call("POST", "/auth/login", "", map[string]any{"email": email, "password": "Passw0rd123"}, nil)
		return r.Status, d, err
	}))

	// authenticated read: cart
	tok := register(9999)
	call("POST", "/cart/items", tok, map[string]any{"product_id": ids[0], "quantity": 1}, nil)
	rep.Load = append(rep.Load, load("Keranjang: GET /cart (JWT + DB)", "GET /cart", 50, 15*time.Second, func(int) (int, time.Duration, error) {
		r, d, err := call("GET", "/cart", tok, nil, nil)
		return r.Status, d, err
	}))

	// --- ramp: throughput vs concurrency ----------------------------------
	for _, c := range []int{1, 10, 25, 50, 100, 200, 400} {
		rep.Ramp = append(rep.Ramp, load(fmt.Sprintf("Ramp list produk c=%d", c), "GET /products?page=1&limit=20", c, 8*time.Second, get("/products?page=1&limit=20")))
	}

	// --- no cache: Redis stopped (fail-open) -------------------------------
	exec.Command("redis-cli", "shutdown", "nosave").Run()
	time.Sleep(time.Second)
	rep.NoCache = append(rep.NoCache,
		load("Tanpa Redis: list produk", "GET /products?page=1&limit=20", 50, 15*time.Second, get("/products?page=1&limit=20")),
		load("Tanpa Redis: detail produk", "GET /products/:id", 50, 15*time.Second, detail),
	)
	exec.Command("redis-server", "--daemonize", "yes").Run()
	time.Sleep(2 * time.Second)

	// --- concurrency correctness -------------------------------------------
	rep.Concurrency = append(rep.Concurrency, oversell(admin, cat), paymentIdempotency(admin, ids), doubleCheckout(ids))

	rep.Environment["finished_at"] = time.Now().Format(time.RFC3339)
	b, _ := json.MarshalIndent(rep, "", "  ")
	os.WriteFile(os.Args[1], b, 0o644)
	fmt.Println("written", os.Args[1])
}

// 300 buyers race for a product with stock 50.
func oversell(admin, cat string) map[string]any {
	const buyers, stock = 300, 50
	p := data(mustCall(0, "POST", "/products", admin, map[string]any{"name": "Flash Sale Item", "description": "", "price": 99000, "stock": stock, "category_id": cat}, nil))
	pid := p["id"].(string)
	tokens := make([]string, buyers)
	parallel(buyers, 16, func(i int) { tokens[i] = register(i) })
	parallel(buyers, 32, func(i int) {
		mustCall(200, "POST", "/cart/items", tokens[i], map[string]any{"product_id": pid, "quantity": 1}, nil)
	})

	var ok, conflict, other int64
	var lat []time.Duration
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, d, err := call("POST", "/orders", tokens[i], nil, nil)
			mu.Lock()
			lat = append(lat, d)
			mu.Unlock()
			switch {
			case err == nil && r.Status == 201:
				atomic.AddInt64(&ok, 1)
			case err == nil && r.Status == 409:
				atomic.AddInt64(&conflict, 1)
			default:
				atomic.AddInt64(&other, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	wall := time.Since(t0)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	final := data(mustCall(0, "GET", "/products/"+pid, "", nil, nil))
	out, _ := exec.Command("psql", "-h", "localhost", "-U", "postgres", "-d", "ecommerce", "-tAc",
		fmt.Sprintf("select coalesce(sum(quantity),0) from order_items where product_id='%s'", pid)).Output()
	res := map[string]any{
		"name": "Rebutan stok (anti-overselling)", "buyers": buyers, "stock": stock,
		"orders_created": ok, "rejected_409": conflict, "other": other,
		"final_stock": final["stock"], "units_sold_db": string(bytes.TrimSpace(out)),
		"wall_ms": wall.Milliseconds(), "p50_ms": pct(lat, .5), "p95_ms": pct(lat, .95), "p99_ms": pct(lat, .99),
		"pass": ok == stock && conflict == buyers-stock && other == 0 && fmt.Sprint(final["stock"]) == "0",
	}
	fmt.Println("oversell:", res)
	return res
}

// 100 concurrent POST /payments with the same key, then 50 concurrent confirms.
func paymentIdempotency(admin string, ids []string) map[string]any {
	tok := register(7777)
	mustCall(200, "POST", "/cart/items", tok, map[string]any{"product_id": ids[1], "quantity": 2}, nil)
	o := data(mustCall(201, "POST", "/orders", tok, nil, nil))
	oid := o["id"].(string)
	key := fmt.Sprintf("idem-%d", time.Now().UnixNano())

	const n = 100
	paymentIDs := sync.Map{}
	codes := map[int]int{}
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, _, err := call("POST", "/payments", tok, map[string]any{"order_id": oid}, map[string]string{"Idempotency-Key": key})
			mu.Lock()
			codes[r.Status]++
			mu.Unlock()
			if err == nil && (r.Status == 200 || r.Status == 201) {
				paymentIDs.Store(data(r)["id"], true)
			}
		}()
	}
	close(start)
	wg.Wait()
	unique := 0
	var pid string
	paymentIDs.Range(func(k, _ any) bool { unique++; pid = k.(string); return true })

	// 50 concurrent confirmations of the same payment
	confirm := map[int]int{}
	start2 := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start2
			r, _, _ := call("POST", "/payments/"+pid+"/simulate-success", tok, nil, nil)
			mu.Lock()
			confirm[r.Status]++
			mu.Unlock()
		}()
	}
	close(start2)
	wg.Wait()
	ord := data(mustCall(0, "GET", "/orders/"+oid, tok, nil, nil))
	out, _ := exec.Command("psql", "-h", "localhost", "-U", "postgres", "-d", "ecommerce", "-tAc",
		fmt.Sprintf("select count(*) from payments where order_id='%s'", oid)).Output()
	res := map[string]any{
		"name": "Idempotency pembayaran", "requests": n, "status_codes": codes, "unique_payment_ids": unique,
		"payments_in_db": string(bytes.TrimSpace(out)), "confirm_requests": 50, "confirm_status_codes": confirm,
		"order_status": ord["status"],
		"pass":         unique == 1 && string(bytes.TrimSpace(out)) == "1" && ord["status"] == "PAID",
	}
	fmt.Println("idempotency:", res)
	return res
}

// One user taps checkout 20 times at once.
func doubleCheckout(ids []string) map[string]any {
	tok := register(8888)
	mustCall(200, "POST", "/cart/items", tok, map[string]any{"product_id": ids[2], "quantity": 1}, nil)
	codes := map[int]int{}
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, _, _ := call("POST", "/orders", tok, nil, nil)
			mu.Lock()
			codes[r.Status]++
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	res := map[string]any{"name": "Checkout ganda (20 tap bersamaan)", "requests": 20, "status_codes": codes, "pass": codes[201] == 1 && codes[400] == 19}
	fmt.Println("double checkout:", res)
	return res
}
