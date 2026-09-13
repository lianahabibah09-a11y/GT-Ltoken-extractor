// Clone growtopia-automation.exe - 3 mode terpisah (flow B: panen vs refresh):
//
//	login   : panen SID Google -> profiles/<email>/ ; exit begitu SID muncul.
//	harvest : panen SID + bank N tokenUrl logon-name (BELUM consumed) -> urls.txt.
//	refresh : replay banked tokenUrl via HTTP murni (NOL Google/browser);
//	          invalid/habis -> fallback browser SID silent-flow (?validate).
//
// Bank line urls.txt: email|url|mac|rid|wk ; prefix "!" = consumed/invalid.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"growtopia-automation/pkg/growtopia"
)

// Config config.json: {"login_mode":"auto"|"manual"}. manual = jendela browser
// dibiarkan, USER login sendiri (akun lama/2FA/captcha); bot cuma pantau SID.
type Config struct {
	LoginMode string `json:"login_mode"`
}

func loadConfig() Config {
	c := Config{LoginMode: "auto"}
	b, err := os.ReadFile("config.json")
	if err != nil {
		return c
	}
	json.Unmarshal(b, &c)
	if c.LoginMode != "manual" {
		c.LoginMode = "auto"
	}
	return c
}

var cfg = loadConfig()

type Account struct {
	Email    string
	Password string
}

func readAccounts(path string) []Account {
	f, err := os.Open(path)
	if err != nil {
		fmt.Println("failed to read accounts:", err)
		return nil
	}
	defer f.Close()
	var out []Account
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimSpace(sc.Text()), "|", 2)
		if parts[0] != "" {
			a := Account{Email: parts[0]}
			if len(parts) == 2 {
				a.Password = parts[1]
			}
			out = append(out, a)
		}
	}
	return out
}

// ---- bank tokenUrl (urls.txt) ----

var urlsFile = "urls.txt"

var bankHTTP = &http.Client{Timeout: 30 * time.Second}

const bankUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1_1 like Mac OS X) " +
	"AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"

type bankEntry struct {
	URL, MAC, RID, WK string
}

// readBank - entry BELUM consumed milik email.
func readBank(email string) []bankEntry {
	f, err := os.Open(urlsFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []bankEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "!") {
			continue
		}
		p := strings.Split(l, "|")
		if len(p) == 5 && p[0] == email {
			out = append(out, bankEntry{URL: p[1], MAC: p[2], RID: p[3], WK: p[4]})
		}
	}
	return out
}

func saveURL(email, u, mac, rid, wk string) error {
	f, err := os.OpenFile(urlsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s|%s|%s|%s|%s\n", email, u, mac, rid, wk)
	return err
}

// markBad - tokenUrl single-use: yang sudah difetch pasti mati (sukses/invalid).
func markBad(email, u string) {
	b, err := os.ReadFile(urlsFile)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		t := strings.TrimSuffix(l, "\r")
		if strings.HasPrefix(t, "!") {
			continue
		}
		if p := strings.Split(t, "|"); len(p) >= 2 && p[0] == email && p[1] == u {
			lines[i] = "!" + t
		}
	}
	os.WriteFile(urlsFile, []byte(strings.Join(lines, "\n")), 0o644)
}

// tryValidate - replay tokenUrl TANPA browser. UA iPhone = proven (gt_bot cmd_refresh).
func tryValidate(u string) (string, bool) {
	if !strings.Contains(u, "validate") {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "validate"
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", bankUA)
	resp, err := bankHTTP.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return growtopia.ParseValidate(string(b))
}

// nextLoginURL - meta + loginURL segar (wk/rid/mac baru tersimpan di h).
func nextLoginURL(h *growtopia.LoginHandler) (string, error) {
	if err := h.GetMetaLogin(); err != nil {
		return "", err
	}
	return h.GetLoginURL()
}

// saveSetSIDAccount - fungsi yang SUDAH ADA di binary asli, diaktifkan lagi:
// simpan cookie sesi Google per akun ke profiles/<email>/ + sid dump.
func saveSetSIDAccount(email string, cookies []*network.Cookie) error {
	dir := filepath.Join("profiles", email)
	os.MkdirAll(dir, 0o755)
	var rows []map[string]any
	for _, c := range cookies {
		rows = append(rows, map[string]any{
			"name": c.Name, "value": c.Value, "domain": c.Domain,
			"path": c.Path, "expires": c.Expires, "httpOnly": c.HTTPOnly, "secure": c.Secure,
		})
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return os.WriteFile(filepath.Join(dir, "sid.json"), b, 0o644)
}

const fillEmailJS = `(function(){
  var i=[...document.querySelectorAll('input[name="identifier"],#identifierId')].find(e=>e.offsetParent);
  if(!i) return 'NO_EMAIL_FIELD';
  var s=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
  s.call(i,'%s');
  i.dispatchEvent(new Event('input',{bubbles:true}));
  i.dispatchEvent(new Event('change',{bubbles:true}));
  return 'OK';})()`

const fillPassJS = `(function(){
  var i=[...document.querySelectorAll('input[type="password"]')].find(e=>e.offsetParent);
  if(!i) return 'NO_PASS_FIELD';
  var s=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
  s.call(i,'%s');
  i.dispatchEvent(new Event('input',{bubbles:true}));
  i.dispatchEvent(new Event('change',{bubbles:true}));
  return 'OK';})()`

// allocOpts - opsi browser sama utk login & refresh.
func allocOpts(profile string, headless bool) []chromedp.ExecAllocatorOption {
	execPath := "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"
	if _, err := os.Stat(execPath); err != nil {
		execPath = `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	}
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.UserDataDir(profile),
		chromedp.Flag("headless", headless),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		// binary asli: UA iPhone + mobile -> GT/Google render form beneran
		// desktop UA = seperti binary asli (curl dump: Edg/152 desktop).
		// iPhone UA bikin Google chooser = SPA yang abai klik sintetis.
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36 Edg/152.0.0.0"),
		chromedp.Flag("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36 Edg/152.0.0.0"),
		chromedp.WindowSize(900, 1200),
	)
}

func processAccount(acc Account, idx int, headless bool, mode string, bank int, manual bool) error {
	fmt.Printf("[%d] Processing account %s (%s%s)\n", idx, acc.Email, mode,
		map[bool]string{true: ", manual"}[manual])
	if manual {
		headless = false // user harus lihat jendelanya
	}

	// refresh: bank dulu — HTTP murni, nol Google, nol browser.
	if mode == "refresh" {
		for _, e := range readBank(acc.Email) {
			tok, ok := tryValidate(e.URL)
			markBad(acc.Email, e.URL) // single-use: sukses/invalid sama-sama mati
			if ok {
				return writeTokenFull(acc, tok, e.MAC, e.RID, e.WK, idx)
			}
			fmt.Printf("[%d] banked URL invalid -> fallback SID browser\n", idx)
		}
	}

	h := growtopia.NewLoginHandler()
	loginURL, err := nextLoginURL(h)
	if err != nil {
		return err
	}

	// profil persistent per email; browser: Chrome asli dulu (binary asli pakai
	// chrome.exe), fallback Edge. PENTING: user-data-dir WAJIB absolut —
	// relatif bikin chromedp "websocket url timeout reached" (terbukti di probe).
	rel := filepath.Join("profiles", acc.Email)
	os.MkdirAll(rel, 0o755)
	profile, err := filepath.Abs(rel)
	if err != nil {
		return err
	}
	opts := allocOpts(profile, headless)
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	var copts []chromedp.ContextOption
	if os.Getenv("CLONE_DEBUG") != "" {
		copts = append(copts, chromedp.WithDebugf(func(f string, v ...interface{}) {
			fmt.Fprintf(os.Stderr, "CDP: "+f+"\n", v...)
		}))
	}
	ctx, cancel := chromedp.NewContext(actx, copts...)
	defer cancel()
	tmo := 7*time.Minute + time.Duration(bank)*2*time.Minute
	if manual {
		tmo = 10 * time.Minute // ketikan manual + 2FA butuh waktu
	}
	ctx, cancelT := context.WithTimeout(ctx, tmo)
	defer cancelT()
	fmt.Printf("[%d] Navigating to login URL...\n", idx)
	if manual {
		fmt.Printf("[%d] MANUAL MODE: login sendiri di jendela Chrome yang terbuka (email/sandi/2FA/captcha).\n", idx)
		fmt.Printf("[%d] Bot otomatis lanjut begitu cookie SID Google terdeteksi (maks 10 menit).\n", idx)
	}

	var haveSID func() bool
	_ = haveSID

	// WAJIB lewat tokenUrl (loginURL): di dalamnya ter-encode packet wk/rid/mac
	// segar dari POST dashboard. /google/redirect = sesi lama -> validate
	// "Token is invalid". SID di profil = form/consent Google auto-lewati.
	err = chromedp.Run(ctx,
		chromedp.Navigate(loginURL),
		// halaman dashboard GT -> klik tombol Google (teks "Continue with Google")
		chromedp.WaitReady(`body`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			if mode != "login" {
				return nil
			}
			return clickGoogleButton(ctx)
		}),
		chromedp.Sleep(3*time.Second),
	)
	if err != nil {
		return fmt.Errorf("navigate: %w", err)
	}

	// loop state machine Google: email -> next -> pass -> next -> consent -> pantau SID
	sidSeen := false
	banked := 0
	var vURL string
	nameDone := false
	tries := 0
	for i := 0; i < 100; i++ {
		var url string
		if err := chromedp.Run(ctx, chromedp.Location(&url)); err != nil {
			return err
		}
		if i%3 == 0 {
			fmt.Printf("[%d] #%d url=%s\n", idx, i, url[:min(400, len(url))])
		}

		// GOAL BARU: SID + PSID muncul = aset tertangkap
		cks, err := getCookies(ctx)
		if err == nil {
			names := map[string]bool{}
			for _, c := range cks {
				names[c.Name] = true
			}
			if names["SID"] && names["__Secure-1PSID"] {
				if !sidSeen {
					sidSeen = true
					fmt.Printf("[%d] Successfully captured SID for %s\n", idx, acc.Email)
					if err := saveSetSIDAccount(acc.Email, cks); err != nil {
						return err
					}
					fmt.Printf("[%d] Saved account to profiles\\%s (sid.json)\n", idx, acc.Email)
					if mode == "login" {
						return nil
					}
				}
				// refresh: SID sudah ada tapi belum consent-finish -> lanjut
			}
		}

		if manual && (mode == "login" || !sidSeen) {
			// manual: bot buta form, user isi sendiri. SID muncul = fase form selesai
			// (login: langsung exit; harvest/refresh: lanjut auto utk tokenUrl).
			if sidSeen {
				return nil
			}
			chromedp.Run(ctx, chromedp.Sleep(2*time.Second))
			continue
		}
		u := strings.ToLower(url)
		// token bisa muncul di URL mana pun (halaman GT setelah consent) -> scan body
		if mode == "refresh" || mode == "harvest" {
			// logon-name/<JWT> butuh ?validate (= curl artefak binary) -> JSON
			if strings.Contains(url, "logon-name/") && !strings.Contains(url, "validate") {
				if mode == "harvest" {
					if err := saveURL(acc.Email, url, h.MAC, h.RID, h.WK); err != nil {
						return err
					}
					banked++
					fmt.Printf("[%d] banked %d/%d untuk %s\n", idx, banked, bank, acc.Email)
					if banked >= bank {
						return nil
					}
					// URL berikutnya: loginURL segar; SID di profile bikin Google silent-approve
					nu, err := nextLoginURL(h)
					if err != nil {
						return err
					}
					chromedp.Run(ctx, chromedp.Sleep(3*time.Second), chromedp.Navigate(nu))
					chromedp.Run(ctx, chromedp.Sleep(2*time.Second))
					continue
				}
				vURL = url + "?validate"
				fmt.Printf("[%d] appending ?validate\n", idx)
				chromedp.Run(ctx, chromedp.Navigate(vURL))
				chromedp.Run(ctx, chromedp.Sleep(2*time.Second))
				continue
			}
			var bt string
			chromedp.Run(ctx, chromedp.Text(`body`, &bt))
			if strings.Contains(bt, "Choose your name") {
				// akun belum punya GrowID -> isi nama random, submit, lanjut
				nm := fmt.Sprintf("np%s%03d", time.Now().Format("0102"), rand.Intn(1000))
				var sr string
				chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(function(){
					var i=document.querySelector('input[name=logonName]');
					if(!i)return 'NOFIELD';
					var s=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
					s.call(i,'%s'); i.dispatchEvent(new Event('input',{bubbles:true}));
					var f=i.closest('form'); if(f){f.submit();return 'SUBMITTED'}
					var b=document.querySelector('input[type=submit]'); if(b){b.click();return 'CLICKED'}
					return 'NOSUBMIT'})()`, nm), &sr))
				fmt.Printf("[%d] name page -> %s (%s), waiting...\n", idx, sr, nm)
				nameDone = true
				chromedp.Run(ctx, chromedp.Sleep(4*time.Second))
				continue
			}
			if strings.Contains(strings.ToLower(url), "validate") && bt != "" {
				os.WriteFile("validate_body.txt", []byte(bt), 0o644)
			}
			if lt, ok := growtopia.ParseValidate(bt); ok {
				return writeToken(acc, h, lt, idx)
			}
			if strings.Contains(bt, "Token is invalid") && nameDone && tries < 2 {
				// tokenUrl single-use habis (termakan form GrowID) -> flow baru
				tries++
				nameDone = false
				fmt.Printf("[%d] re-run flow (#%d)...\n", idx, tries)
				nu, err := nextLoginURL(h)
				if err != nil {
					return err
				}
				chromedp.Run(ctx, chromedp.Sleep(5*time.Second), chromedp.Navigate(nu))
				chromedp.Run(ctx, chromedp.Sleep(2*time.Second))
				continue
			}
			if strings.Contains(bt, "too many people") && vURL != "" {
				fmt.Printf("[%d] GT rate-limit, tunggu 35s, retry vURL...\n", idx)
				chromedp.Run(ctx, chromedp.Sleep(35*time.Second), chromedp.Navigate(vURL))
			}
		}
		switch {
		case strings.Contains(u, "captcha") || strings.Contains(u, "sorry"):
			return fmt.Errorf("captcha detected for %s (akun/IP kena flag Google, solusinya: non-headless atau IP tetap)", acc.Email)
		case strings.Contains(u, "signin/rejected"):
			return fmt.Errorf("google REJECTED %s (wrong/nonexistent account or bot-flag)", acc.Email)
		case strings.Contains(u, "oauth/id") || strings.Contains(u, "chooser"):
			// akun = div[role=link] jsaction click -> butuh mouse event CDP asli
			sel := fmt.Sprintf(`[data-identifier="%s"]`, acc.Email)
			if err := chromedp.Run(ctx,
				chromedp.WaitVisible(sel, chromedp.ByQuery),
				chromedp.Sleep(400*time.Millisecond),
				chromedp.Click(sel, chromedp.ByQuery),
			); err != nil {
				fmt.Printf("[%d] chooser click err: %v\n", idx, err)
			} else {
				fmt.Printf("[%d] chooser clicked account\n", idx)
			}
			chromedp.Run(ctx, chromedp.Sleep(1200*time.Millisecond))
			var st string
			chromedp.Run(ctx, chromedp.Evaluate(`(function(){
				var a=document.querySelector('[data-identifier]');
				var seq=['pointerdown','mousedown','pointerup','mouseup','click'];
				var el=document.querySelector('[jsname="MBVUVe"]')||a;
				if(!el)return 'NOEL';
				for(var t of seq){el.dispatchEvent(new PointerEvent(t,{bubbles:true,cancelable:true,view:window}))}
				var b=[...document.querySelectorAll('div,button,a,span')].find(function(e){return /^Continue|Lanjutkan|Berikut$/i.test(e.textContent.trim())&&e.offsetHeight>10&&e.offsetHeight<100});
				if(b){for(var t of seq){b.dispatchEvent(new PointerEvent(t,{bubbles:true,cancelable:true,view:window}))}return 'CLICKED-BTN:'+b.tagName}
				return 'NOBTN AFTER:'+!!b})()`, &st))
			fmt.Printf("[%d] chooser seq: %s\n", idx, st)
			var hk string
			chromedp.Run(ctx, chromedp.Evaluate(`document.body.outerHTML.slice(0,0)`, &hk))
			chromedp.Run(ctx, chromedp.Evaluate(`document.body.innerHTML`, &hk))
			os.WriteFile(fmt.Sprintf("chooser_after_%d.html", i), []byte(hk), 0o644)
		// consent = halaman "Sign in to X" / services/oauth, BUKAN /signin/identifier
		// (kata "consent" nyasar di URL identifier = bug lama: Next kosong diklik)
		case strings.Contains(u, "consent/generic") || strings.Contains(u, "services/oauth") ||
			strings.Contains(u, "checkptls") || strings.Contains(u, "v3/signin/consent"):
			var cr string
			chromedp.Run(ctx, chromedp.Evaluate(`(function(){
				var b=[...document.querySelectorAll('#submit-button,#continue-button,#next,button[type=submit],[jsname="LgbsSe"],[aria-label*="Continue" i],[aria-label*="Lanjutkan" i]')]
					.find(e=>e.offsetParent&&e.offsetWidth>10);
				if(b){b.click();return 'CLICKED'}return 'NOBTN'})()`, &cr))
			fmt.Printf("[%d] consent: %s\n", idx, cr)
		case strings.Contains(u, "challenge/pwd") || strings.Contains(u, "challenge/otp"):
			if mode == "refresh" {
				return fmt.Errorf("SID_MATI %s: Google tantang password lagi -> harvest/login ulang", acc.Email)
			}
			// password dulu (input VISIBEL), jangan ketipu hidden identifier
			var res string
			if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(fillPassJS, jsEsc(acc.Password)), &res)); err == nil && res == "OK" {
				fmt.Printf("[%d] Successfully filled password\n", idx)
				chromedp.Run(ctx, chromedp.Sleep(700*time.Millisecond))
				nativeNext(ctx, "passwordNext")
			} else {
				fmt.Printf("[%d] pwd page, no field yet (%s)\n", idx, res)
			}
		case strings.Contains(u, "signin/identifier") || strings.Contains(u, "service/login") ||
			strings.Contains(u, "_gaia") || strings.Contains(u, "accountchooser"):
			if mode == "refresh" {
				return fmt.Errorf("SID_MATI %s: Google minta form login lagi -> harvest/login ulang", acc.Email)
			}
			// halaman email?
			var res string
			if err := chromedp.Run(ctx,
				chromedp.Evaluate(fmt.Sprintf(fillEmailJS, jsEsc(acc.Email)), &res)); err == nil && res == "OK" {
				fmt.Printf("[%d] Successfully filled email\n", idx)
				chromedp.Run(ctx, chromedp.Sleep(700*time.Millisecond))
				nativeNext(ctx, "identifierNext")
			} else {
				fmt.Printf("[%d] Google page state waiting... %s\n", idx, u[:min(70, len(u))])
			}
		}
		chromedp.Run(ctx, chromedp.Sleep(2*time.Second))
	}
	return fmt.Errorf("timeout: SID never appeared for %s", acc.Email)
}

func getCookies(ctx context.Context) ([]*network.Cookie, error) {
	var out []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		dom, err := network.GetCookies().WithURLs([]string{
			"https://accounts.google.com", "https://www.google.com"}).Do(ctx)
		if err != nil {
			return err
		}
		out = dom
		return nil
	}))
	return out, err
}

// nativeNext - klik CDP asli (pointer) utk tombol submit Google v3.
// JS .click() synthetic diabaikan SPA v3 = bug "password diam aja".
// selector: tombol visible ber-jsname LgbsSe / aria-label Next|Continue.
func nativeNext(ctx context.Context, label string) error {
	var box struct{ X, Y float64 }
	var found bool
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`(function(){
			var b=[...document.querySelectorAll('[jsname="LgbsSe"],button[type=submit],[aria-label="Next"],[aria-label="Berikut"],[role="button"]')]
				.find(e=>e.offsetParent&&e.offsetWidth>20&&/^(Next|Continue|Berikut|Lanjutkan|next|continue)$/i.test((e.getAttribute('aria-label')||e.textContent||'').trim().split('\n')[0]));
			if(!b) return null;
			b.scrollIntoView({block:'center'});
			var r=b.getBoundingClientRect();
			return {x:r.x+r.width/2, y:r.y+r.height/2};})()`, &box))
	if err == nil {
		found = box.X > 0 && box.Y > 0
	}
	if found {
		err = chromedp.Run(ctx,
			chromedp.MouseClickXY(box.X, box.Y),
			chromedp.Sleep(300*time.Millisecond),
			chromedp.ActionFunc(func(ctx context.Context) error {
				return input.DispatchMouseEvent(input.MouseReleased, box.X, box.Y).Do(ctx)
			}))
		if err == nil {
			fmt.Printf("[*] nativeNext %s: CDP-click (%.0f,%.0f)\n", label, box.X, box.Y)
			return nil
		}
	}
	// fallback JS lama utk page non-v3
	return chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`(function(){
		var b=[...document.querySelectorAll('#%[1]s,[jsname="LgbsSe"],button[type=submit]')]
			.find(e=>e&&e.offsetParent);
		if(b){b.click();return 'JSCLICK'}return 'NOBTN'})()`, label), new(string)))
}

func clickGoogleButton(ctx context.Context) error {
	return chromedp.Evaluate(`
		(function(){
		  var els=[...document.querySelectorAll('button,a,div[role=button]')];
		  var b=els.find(e=>/google/i.test(e.textContent||''));
		  if(b){b.click();return 'CLICKED'}
		  return 'NOT_FOUND'})()`, new(string)).Do(ctx)
}

func clickID(sel string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(
		`(function(){var b=document.querySelector('%s');if(b){b.click();return 1}return 0})()`, sel),
		new(int))
}

// writeTokenFull - append email|ltoken|mac|rid|wk. mac/rid/wk = pasangan packet
// tokenUrl aslinya (banked URL punya packet sendiri, BUKAN h terkini).
func writeTokenFull(acc Account, ltoken, mac, rid, wk string, idx int) error {
	fmt.Printf("[%d] Successfully converted token URL\n", idx)
	out := fmt.Sprintf("%s|%s|%s|%s|%s\n", acc.Email, ltoken, mac, rid, wk)
	f, err := os.OpenFile("tokens.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(out); err != nil {
		return err
	}
	fmt.Printf("TOKEN %s", out)
	return nil
}

func writeToken(acc Account, h *growtopia.LoginHandler, ltoken string, idx int) error {
	return writeTokenFull(acc, ltoken, h.MAC, h.RID, h.WK, idx)
}

func jsEsc(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// defaultMode diset via -ldflags "-X main.defaultMode=harvest" per binary.
var defaultMode = "login"

func main() {
	headless := false
	mode := defaultMode
	bank := 3
	file := "accounts.txt"
	manual := cfg.LoginMode == "manual"
	// clone.exe [login|harvest|refresh] [accounts.txt] [--headless] [--bank=N] [--auto|--manual]
	for _, a := range os.Args[1:] {
		switch {
		case a == "--headless":
			headless = true
		case a == "--manual":
			manual = true
		case a == "--auto":
			manual = false
		case a == "--refresh": // flag lama tetap jalan
			mode = "refresh"
		case a == "login" || a == "harvest" || a == "refresh":
			mode = a
		case strings.HasPrefix(a, "--bank="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--bank="))
			if err != nil || n < 1 {
				n = 3
			}
			bank = n
		default:
			file = a
		}
	}
	accs := readAccounts(file)
	if len(accs) == 0 {
		fmt.Println("no accounts in", file, "(mode="+mode+")")
		os.Exit(1)
	}
	ok, fail := 0, 0
	for i, a := range accs {
		if err := processAccount(a, i, headless, mode, bank, manual); err != nil {
			fmt.Printf("[%d] FAILED %s: %v\n", i, a.Email, err)
			fail++
		} else {
			ok++
		}
	}
	fmt.Printf("done: %d ok, %d failed (mode=%s)\n", ok, fail, mode)
}
