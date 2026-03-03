package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ─── CONFIG ──────────────────────────────────────────────────────────────────

const (
	repoOwner    = "SimpleNiQue"
	repoIsOrg    = false
	cacheTimeout = 60 * time.Second
	forgeRepo    = "forge"
)

// Add new templates here as you create them
var templates = map[string]string{
	"django":  "django-backend-template",
	"golang":  "golang-backend-template",
	"nextjs":  "nextjs-template",
	"fastapi": "fastapi-backend-template",
}

// version is injected at build time:
//   go build -ldflags="-X main.version=v1.0.0"
var version = "dev"

// ─── PATHS ───────────────────────────────────────────────────────────────────

func configBaseDir() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(base, "forge")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "forge")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(os.Getenv("HOME"), ".config")
		}
		return filepath.Join(base, "forge")
	}
}

func cacheDir(templateName string) string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(base, "forge", "cache", templateName)
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Caches", "forge", templateName)
	default:
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			base = filepath.Join(os.Getenv("HOME"), ".cache")
		}
		return filepath.Join(base, "forge", templateName)
	}
}

func authFilePath() string {
	return filepath.Join(configBaseDir(), "auth.json")
}

func metaFilePath(templateName string) string {
	return filepath.Join(cacheDir(templateName), ".meta.json")
}

// ─── AUTH ────────────────────────────────────────────────────────────────────

type AuthConfig struct {
	Token    string    `json:"token"`
	Username string    `json:"username"`
	SavedAt  time.Time `json:"saved_at"`
}

func loadAuth() (*AuthConfig, error) {
	data, err := os.ReadFile(authFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not logged in — run: forge auth login")
		}
		return nil, err
	}
	var auth AuthConfig
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("corrupt auth file — run: forge auth login")
	}
	if auth.Token == "" {
		return nil, fmt.Errorf("no token stored — run: forge auth login")
	}
	return &auth, nil
}

func saveAuth(auth *AuthConfig) error {
	if err := os.MkdirAll(configBaseDir(), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(authFilePath(), data, 0600)
}

func deleteAuth() error {
	err := os.Remove(authFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func verifyToken(token string) (string, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("invalid token — check it and try again")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API error: HTTP %d", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("could not parse GitHub response")
	}

	if repoIsOrg {
		return verifyOrgAccess(token, user.Login)
	}
	return verifyPersonalAccess(token, user.Login)
}

func verifyPersonalAccess(token string, username string) (string, error) {
	if !strings.EqualFold(username, repoOwner) {
		return "", fmt.Errorf(
			"access denied — forge is private to @%s (you are @%s)",
			repoOwner, username,
		)
	}
	return username, nil
}

func verifyOrgAccess(token string, username string) (string, error) {
	orgCheckURL := fmt.Sprintf("https://api.github.com/orgs/%s/members/%s", repoOwner, username)
	req, _ := http.NewRequest("GET", orgCheckURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("network error checking org membership: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 {
		return username, nil
	}

	// Fallback: check repo access directly
	repoCheckURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", repoOwner, forgeRepo)
	req2, _ := http.NewRequest("GET", repoCheckURL, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Accept", "application/vnd.github+json")

	resp2, err := (&http.Client{Timeout: 10 * time.Second}).Do(req2)
	if err != nil {
		return "", fmt.Errorf("network error checking repo access: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode == 200 {
		return username, nil
	}

	return "", fmt.Errorf(
		"access denied — @%s is not a member of the '%s' org",
		username, repoOwner,
	)
}

// ─── AUTHENTICATED HTTP ───────────────────────────────────────────────────────

func authedGet(url string, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return (&http.Client{Timeout: cacheTimeout}).Do(req)
}

func isOnline(token string) bool {
	resp, err := authedGet("https://api.github.com", token)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// ─── METADATA ────────────────────────────────────────────────────────────────

type FileMeta struct {
	SHA string `json:"sha"`
}

type CacheMeta struct {
	LastChecked time.Time            `json:"last_checked"`
	Files       map[string]*FileMeta `json:"files"`
}

func loadMeta(templateName string) *CacheMeta {
	data, err := os.ReadFile(metaFilePath(templateName))
	if err != nil {
		return &CacheMeta{Files: make(map[string]*FileMeta)}
	}
	var m CacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return &CacheMeta{Files: make(map[string]*FileMeta)}
	}
	if m.Files == nil {
		m.Files = make(map[string]*FileMeta)
	}
	return &m
}

func saveMeta(templateName string, m *CacheMeta) {
	m.LastChecked = time.Now()
	data, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(metaFilePath(templateName), data, 0644)
}

// ─── GITHUB TREE ─────────────────────────────────────────────────────────────

type GithubTreeItem struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type GithubTreeResponse struct {
	Tree []GithubTreeItem `json:"tree"`
}

func fetchRemoteTree(repoName string, token string) ([]GithubTreeItem, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/git/trees/main?recursive=1",
		repoOwner, repoName,
	)
	resp, err := authedGet(url, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("auth error (%d) — run: forge auth login", resp.StatusCode)
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("template repo '%s/%s' not found", repoOwner, repoName)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var tree GithubTreeResponse
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, err
	}
	return tree.Tree, nil
}

func fetchFileContent(repoName string, path string, token string) ([]byte, error) {
	url := fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/main/%s",
		repoOwner, repoName, path,
	)
	resp, err := authedGet(url, token)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch %s: HTTP %d", path, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ─── CACHE SYNC ──────────────────────────────────────────────────────────────

func syncCache(templateName string, repoName string, token string, verbose bool) error {
	fmt.Printf("🌐 Checking for updates to '%s' template...\n", templateName)

	tree, err := fetchRemoteTree(repoName, token)
	if err != nil {
		return fmt.Errorf("fetching repo tree: %w", err)
	}

	meta := loadMeta(templateName)
	cDir := cacheDir(templateName)
	updated, added := 0, 0

	for _, item := range tree {
		if item.Type != "blob" {
			continue
		}

		localPath := filepath.Join(cDir, filepath.FromSlash(item.Path))
		cachedMeta, exists := meta.Files[item.Path]

		needsUpdate := !exists || cachedMeta.SHA != item.SHA
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			needsUpdate = true
		}
		if !needsUpdate {
			continue
		}

		content, err := fetchFileContent(repoName, item.Path, token)
		if err != nil {
			fmt.Printf("  ⚠️  Could not fetch %s: %v\n", item.Path, err)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(localPath, content, 0644); err != nil {
			return err
		}

		if verbose {
			if exists {
				fmt.Printf("  🔄 Updated: %s\n", item.Path)
				updated++
			} else {
				fmt.Printf("  ✅ Added:   %s\n", item.Path)
				added++
			}
		}
		meta.Files[item.Path] = &FileMeta{SHA: item.SHA}
	}

	saveMeta(templateName, meta)

	if updated+added == 0 {
		fmt.Printf("✅ '%s' template is already up to date.\n", templateName)
	} else {
		fmt.Printf("✅ '%s' sync complete — %d added, %d updated.\n", templateName, added, updated)
	}
	return nil
}

func ensureCacheExists(templateName string, repoName string, token string) error {
	cDir := cacheDir(templateName)
	if _, err := os.Stat(cDir); os.IsNotExist(err) {
		fmt.Printf("📦 First use of '%s' template — downloading from GitHub...\n", templateName)
		if err := os.MkdirAll(cDir, 0755); err != nil {
			return err
		}
		return syncCache(templateName, repoName, token, true)
	}

	if isOnline(token) {
		return syncCache(templateName, repoName, token, false)
	}

	fmt.Println("📴 Offline — using cached template.")
	return nil
}

// ─── SCAFFOLD ────────────────────────────────────────────────────────────────

func scaffold(templateName string, projectName string) error {
	cDir := cacheDir(templateName)

	return filepath.WalkDir(cDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Base(path) == ".meta.json" {
			return nil
		}

		rel, err := filepath.Rel(cDir, path)
		if err != nil {
			return err
		}

		// Rename PROJECT_NAME folder placeholder to actual project name
		rel = strings.ReplaceAll(rel, "PROJECT_NAME", projectName)
		targetPath := filepath.Join(projectName, rel)

		if d.IsDir() {
			fmt.Printf("  📁 %s/\n", rel)
			return os.MkdirAll(targetPath, 0755)
		}
		if filepath.Base(path) == ".gitkeep" {
			return nil
		}

		// Write file contents exactly as-is
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading cached file %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		fmt.Printf("  📄 %s\n", rel)
		return os.WriteFile(targetPath, content, 0644)
	})
}

// ─── UPDATE ──────────────────────────────────────────────────────────────────

type GithubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func runUpdate(_ []string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🔍 Current version : %s\n", version)
	fmt.Println("🌐 Checking for latest release...")

	resp, err := authedGet(
		fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, forgeRepo),
		auth.Token,
	)
	if err != nil {
		fmt.Printf("❌ Network error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("❌ GitHub API returned %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Printf("❌ Could not parse release info: %v\n", err)
		os.Exit(1)
	}

	latest := release.TagName
	fmt.Printf("📦 Latest version  : %s\n", latest)

	if version == latest || (version != "dev" && version > latest) {
		fmt.Println("\n✅ Already up to date!")
		return
	}

	expectedName := fmt.Sprintf("forge-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		expectedName += ".exe"
	}

	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == expectedName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		fmt.Printf("❌ No binary found for %s/%s in release %s\n", runtime.GOOS, runtime.GOARCH, latest)
		os.Exit(1)
	}

	fmt.Printf("\n⬇️  Downloading forge %s...\n", latest)

	binResp, err := authedGet(downloadURL, auth.Token)
	if err != nil {
		fmt.Printf("❌ Download error: %v\n", err)
		os.Exit(1)
	}
	defer binResp.Body.Close()

	newBinary, err := io.ReadAll(binResp.Body)
	if err != nil {
		fmt.Printf("❌ Failed to read binary: %v\n", err)
		os.Exit(1)
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Could not find current executable path: %v\n", err)
		os.Exit(1)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		fmt.Printf("❌ Could not resolve symlinks: %v\n", err)
		os.Exit(1)
	}

	tmpPath := exePath + ".tmp"
	if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
		fmt.Printf("❌ Could not write to %s\n   Try: sudo forge update\n", tmpPath)
		os.Exit(1)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		fmt.Printf("❌ Could not replace binary: %v\n   Try: sudo forge update\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ forge updated %s → %s\n", version, latest)
}

// ─── COMMANDS ────────────────────────────────────────────────────────────────

func readSecret() string {
	sttyOff := exec.Command("stty", "-echo")
	sttyOff.Stdin = os.Stdin
	_ = sttyOff.Run()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	token := strings.TrimSpace(scanner.Text())
	fmt.Println()

	sttyOn := exec.Command("stty", "echo")
	sttyOn.Stdin = os.Stdin
	_ = sttyOn.Run()

	return token
}

func runAuthLogin(_ []string) {
	fmt.Println("🔐 Log in with a GitHub Personal Access Token (PAT)")
	fmt.Println()
	fmt.Println("To create a token:")
	fmt.Println("  1. Go to: https://github.com/settings/tokens/new")
	fmt.Println("  2. Select scopes: repo")
	fmt.Println("  3. Paste the token below")
	fmt.Println()
	fmt.Print("Token: ")

	token := readSecret()

	if token == "" {
		fmt.Println("❌ No token provided.")
		os.Exit(1)
	}

	if repoIsOrg {
		fmt.Printf("⏳ Verifying token and checking membership in @%s...\n", repoOwner)
	} else {
		fmt.Printf("⏳ Verifying token for @%s...\n", repoOwner)
	}

	username, err := verifyToken(token)
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}

	auth := &AuthConfig{Token: token, Username: username, SavedAt: time.Now()}
	if err := saveAuth(auth); err != nil {
		fmt.Printf("❌ Failed to save auth: %v\n", err)
		os.Exit(1)
	}

	if repoIsOrg {
		fmt.Printf("✅ Logged in as @%s — welcome to the %s org!\n", username, repoOwner)
	} else {
		fmt.Printf("✅ Logged in as @%s\n", username)
	}
}

func runAuthLogout(_ []string) {
	auth, err := loadAuth()
	username := "unknown"
	if err == nil {
		username = auth.Username
	}
	if err := deleteAuth(); err != nil {
		fmt.Printf("❌ Failed to remove auth: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("👋 Logged out @%s — auth token removed.\n", username)
}

func runAuthStatus(_ []string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Printf("❌ Not logged in: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("👤 Logged in as : @%s\n", auth.Username)
	fmt.Printf("🕒 Token saved  : %s\n", auth.SavedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("📁 Auth file    : %s\n", authFilePath())
	fmt.Println()
	fmt.Print("⏳ Verifying token is still valid... ")

	if _, err := verifyToken(auth.Token); err != nil {
		fmt.Printf("\n⚠️  Token invalid: %v\n   Run: forge auth login\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Valid")
}

func runStart(args []string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}

	var projectName, templateFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name", "-n":
			if i+1 < len(args) {
				projectName = args[i+1]
				i++
			}
		case "--template", "-t":
			if i+1 < len(args) {
				templateFlag = args[i+1]
				i++
			}
		}
	}

	if templateFlag == "" {
		fmt.Println("❌ --template flag is required")
		fmt.Println("   Usage: forge start --template django --name my_project")
		fmt.Println()
		printTemplates()
		os.Exit(1)
	}

	repoName, ok := templates[templateFlag]
	if !ok {
		fmt.Printf("❌ Unknown template: '%s'\n\n", templateFlag)
		printTemplates()
		os.Exit(1)
	}

	if projectName == "" {
		fmt.Println("❌ --name flag is required")
		fmt.Println("   Usage: forge start --template django --name my_project")
		os.Exit(1)
	}
	if strings.Contains(projectName, " ") {
		fmt.Println("❌ Project name cannot contain spaces.")
		os.Exit(1)
	}
	if _, err := os.Stat(projectName); !os.IsNotExist(err) {
		fmt.Printf("❌ Directory '%s' already exists.\n", projectName)
		os.Exit(1)
	}

	if err := ensureCacheExists(templateFlag, repoName, auth.Token); err != nil {
		fmt.Printf("❌ Cache error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n🚀 Scaffolding '%s' project: %s\n\n", templateFlag, projectName)

	if err := scaffold(templateFlag, projectName); err != nil {
		fmt.Printf("\n❌ Scaffold error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Project '%s' created successfully!\n", projectName)
	fmt.Printf("   cd %s\n\n", projectName)
}

func runSync(args []string) {
	auth, err := loadAuth()
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}

	var templateFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--template", "-t":
			if i+1 < len(args) {
				templateFlag = args[i+1]
				i++
			}
		}
	}

	if templateFlag == "" {
		fmt.Println("🔄 Syncing all templates...")
		for name, repo := range templates {
			if err := syncCache(name, repo, auth.Token, true); err != nil {
				fmt.Printf("⚠️  Failed to sync '%s': %v\n", name, err)
			}
		}
		return
	}

	repoName, ok := templates[templateFlag]
	if !ok {
		fmt.Printf("❌ Unknown template: '%s'\n\n", templateFlag)
		printTemplates()
		os.Exit(1)
	}

	fmt.Printf("🔄 Syncing '%s' template...\n", templateFlag)
	if err := syncCache(templateFlag, repoName, auth.Token, true); err != nil {
		fmt.Printf("❌ Sync failed: %v\n", err)
		os.Exit(1)
	}
}

func runCacheInfo(_ []string) {
	fmt.Println("📦 Cache info:\n")
	for name := range templates {
		cDir := cacheDir(name)
		meta := loadMeta(name)
		status := "✅"
		if _, err := os.Stat(cDir); os.IsNotExist(err) {
			status = "⬜ not downloaded"
		}
		fmt.Printf("  %-12s %s  files: %-4d  last synced: %s\n",
			name, status, len(meta.Files), formatTime(meta.LastChecked))
	}
}

func runCacheClear(args []string) {
	var templateFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--template", "-t":
			if i+1 < len(args) {
				templateFlag = args[i+1]
				i++
			}
		}
	}

	if templateFlag == "" {
		// Clear all — walk up one level from any template cache to get root
		root := filepath.Dir(cacheDir("placeholder"))
		if err := os.RemoveAll(root); err != nil {
			fmt.Printf("❌ Failed to clear cache: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("🗑️  All template caches cleared.")
		return
	}

	if _, ok := templates[templateFlag]; !ok {
		fmt.Printf("❌ Unknown template: '%s'\n\n", templateFlag)
		printTemplates()
		os.Exit(1)
	}

	if err := os.RemoveAll(cacheDir(templateFlag)); err != nil {
		fmt.Printf("❌ Failed to clear cache: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🗑️  '%s' template cache cleared.\n", templateFlag)
}

func printTemplates() {
	fmt.Printf("  %-12s  %s\n", "TEMPLATE", "REPO")
	fmt.Printf("  %-12s  %s\n", "--------", "----")
	for name, repo := range templates {
		fmt.Printf("  %-12s  github.com/%s/%s\n", name, repoOwner, repo)
	}
	fmt.Println()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04:05")
}

func printUsage() {
	fmt.Print(`forge — Scaffold projects from your private GitHub templates

USAGE:
  forge <command> [flags]

COMMANDS:
  auth login              Save your GitHub PAT (required before first use)
  auth logout             Remove saved token
  auth status             Check current login and token validity
  start                   Scaffold a new project from a template
  templates               List all available templates
  update                  Update forge to the latest version
  sync                    Sync template cache from GitHub
  cache info              Show cache status for all templates
  cache clear             Clear template caches

FLAGS (start):
  --template, -t          Template to use (required)
  --name, -n              Project name (required)

FLAGS (sync / cache clear):
  --template, -t          Target a specific template (omit for all)

EXAMPLES:
  forge auth login
  forge start --template django --name my_api
  forge start --template golang --name my_service
  forge start -t nextjs -n my_app
  forge templates
  forge sync
  forge sync --template golang
  forge update

CACHE LOCATION:
  Linux   : ~/.cache/forge/<template>/
  macOS   : ~/Library/Caches/forge/<template>/
  Windows : %APPDATA%\forge\<template>\
`)
}

// ─── MAIN ────────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "auth":
		if len(os.Args) < 3 {
			fmt.Println("Usage: forge auth <login|logout|status>")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "login":
			runAuthLogin(nil)
		case "logout":
			runAuthLogout(nil)
		case "status":
			runAuthStatus(nil)
		default:
			fmt.Printf("❌ Unknown auth subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	case "start":
		runStart(os.Args[2:])
	case "templates":
		printTemplates()
	case "update":
		runUpdate(nil)
	case "sync":
		runSync(os.Args[2:])
	case "cache":
		if len(os.Args) < 3 {
			runCacheInfo(nil)
			return
		}
		switch os.Args[2] {
		case "info":
			runCacheInfo(nil)
		case "clear":
			runCacheClear(os.Args[3:])
		default:
			fmt.Printf("❌ Unknown cache subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Printf("❌ Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}