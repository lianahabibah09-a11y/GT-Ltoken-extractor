package main

import (
	"os"
	"strings"
	"testing"
)

func TestBankRoundtrip(t *testing.T) {
	tmp := t.TempDir() + "/urls.txt"
	old := urlsFile
	urlsFile = tmp
	defer func() { urlsFile = old }()

	u1 := "https://login.growtopiagame.com/logon-name/JWT1"
	u2 := "https://login.growtopiagame.com/logon-name/JWT2"
	if err := saveURL("a@b.c", u1, "m1", "r1", "w1"); err != nil {
		t.Fatal(err)
	}
	saveURL("a@b.c", u2, "m2", "r2", "w2")
	saveURL("x@y.z", u1, "m9", "r9", "w9")

	bank := readBank("a@b.c")
	if len(bank) != 2 || bank[0].MAC != "m1" || bank[1].RID != "r2" {
		t.Fatalf("readBank = %+v", bank)
	}

	markBad("a@b.c", u1)
	bank = readBank("a@b.c")
	if len(bank) != 1 || bank[0].URL != u2 {
		t.Fatalf("after markBad = %+v", bank)
	}
	// baris email lain gak ikut ke-mark
	b, _ := os.ReadFile(tmp)
	if strings.Contains(string(b), "!x@y.z") {
		t.Fatal("cross-account mark")
	}
	// idempotent
	markBad("a@b.c", u1)
	b2, _ := os.ReadFile(tmp)
	if strings.Count(string(b2), "!!") != 0 {
		t.Fatal("double mark")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if c := loadConfig(); c.LoginMode != "auto" { // tanpa file = default auto
		t.Fatalf("no-file = %+v", c)
	}
	os.WriteFile("config.json", []byte(`{"login_mode":"manual"}`), 0o644)
	if c := loadConfig(); c.LoginMode != "manual" {
		t.Fatalf("manual = %+v", c)
	}
	os.WriteFile("config.json", []byte(`{"login_mode":"ngawur"}`), 0o644)
	if c := loadConfig(); c.LoginMode != "auto" { // nilai tak dikenal -> auto
		t.Fatalf("garbage = %+v", c)
	}
}

func TestTryValidateURLAppend(t *testing.T) {
	// URL tanpa ? tak boleh hasilkan "?validate?validate"; cukup cek tidak panic
	// + invalid host => ok=false tanpa network sukses.
	if _, ok := tryValidate("http://127.0.0.1:1/x"); ok {
		t.Fatal("expected fail on dead port")
	}
}
