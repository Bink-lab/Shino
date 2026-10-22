package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Bink-lab/Shino/internal/download"
	"github.com/Bink-lab/Shino/internal/dump"
	"github.com/Bink-lab/Shino/internal/library"
	"github.com/Bink-lab/Shino/internal/lua"
	"github.com/Bink-lab/Shino/internal/tui"
)

func main() {
	luaPath := flag.String("lua", "", "game lua config file")
	manifestsDir := flag.String("manifests-dir", "", "folder with .manifest files")
	output := flag.String("output", "", "target download directory")
	maxDownloads := flag.Int("max-downloads", 16, "parallel chunk downloads per depot")
	doDownload := flag.Bool("download", false, "download after resolving depots")
	dumpMode := flag.Bool("dump", false, "extract owned depot keys and manifests for donation via QR sign-in")
	dumpOutput := flag.String("dump-output", "shino-donation", "output directory for dump mode")
	dumpAppIDs := flag.String("dump-appids", "", "comma-separated appids to dump, defaults to all owned")
	rememberLogin := flag.Bool("remember-login", false, "store the steam sign-in in the os keyring for future runs")
	upload := flag.Bool("upload", false, "upload the donation archive to shinoweb after dumping")
	showVersion := flag.Bool("version", false, "print the build version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(dump.BuildTag())
		return
	}

	if *dumpMode {
		if err := runDump(*dumpOutput, *dumpAppIDs, *rememberLogin, *upload); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *luaPath == "" {
		if _, err := tea.NewProgram(tui.New(), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cfg, err := lua.LoadFile(*luaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("app %d, %d depots (os: %s)\n", cfg.AppID, len(cfg.Depots), download.CurrentOS())
	for _, d := range cfg.Depots {
		status := "no-manifest"
		if d.HasManifest {
			status = fmt.Sprintf("manifest %d", d.ManifestID)
		}
		fmt.Printf("  depot %d key:%t %s os:%s\n", d.ID, d.Key != "", status, download.OSLabel(d.OS))
	}
	if !*doDownload {
		return
	}
	if *output == "" {
		fmt.Fprintln(os.Stderr, "--output required with --download")
		os.Exit(1)
	}
	var mu sync.Mutex
	last := time.Now()
	warnings, err := download.DownloadAll(context.Background(), cfg, *manifestsDir, *output, *maxDownloads, func(depotID uint32, done, total uint64) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < 500*time.Millisecond && done != total {
			return
		}
		last = time.Now()
		fmt.Printf("\rdepot %d %d/%d MB", depotID, done>>20, total>>20)
	}, nil)
	fmt.Println()
	for _, w := range warnings {
		fmt.Println("warn:", w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("done")
}

func runDump(outputDir, appIDsRaw string, remember, upload bool) error {
	filter, err := dump.ParseAppIDList(appIDsRaw)
	if err != nil {
		return err
	}
	ctx := context.Background()
	onStatus := func(line string) { fmt.Println(line) }
	client, err := dump.ConnectStored(ctx, onStatus)
	if err != nil {
		fmt.Println("connecting to steam")
		shown := 0
		client, err = dump.ConnectQR(ctx, func(url string) {
			shown++
			lines, rerr := dump.RenderQR(url)
			if rerr != nil {
				fmt.Println(url)
				return
			}
			if shown > 1 {
				fmt.Println("the code expired and was refreshed, scan the new one")
			}
			for _, line := range lines {
				fmt.Println(line)
			}
			fmt.Println(url)
			fmt.Println("scan with the Steam Mobile app, then approve the sign-in")
		}, onStatus, func(account, refreshToken string) {
			if !remember {
				return
			}
			store := dump.NewKeyringStorePublic()
			if err := store.Save(account, refreshToken); err != nil {
				fmt.Println("could not remember sign-in:", err)
				return
			}
			fmt.Println("sign-in saved for", account, "(forget in the tui with f)")
		})
	}
	if err != nil {
		return err
	}
	defer client.Close()
	summary, err := dump.RunWithClient(ctx, client, dump.Options{
		OutputDir: outputDir,
		Filter:    filter,
		OnLog:     onStatus,
	})
	if err != nil {
		return err
	}
	fmt.Printf("donation ready in %s\n", summary.Archive)
	fmt.Printf("%d apps, %d depots, %d manifests\n", summary.Apps, summary.Depots, summary.Manifests)
	if upload {
		if err := uploadSummary(ctx, summary); err != nil {
			return err
		}
	}
	return nil
}

func uploadSummary(ctx context.Context, summary dump.Summary) error {
	store, err := library.NewKeyStore(library.ShinoKeyFile)
	if err != nil {
		return err
	}
	apiKey, err := store.Load()
	if err != nil || apiKey == "" {
		return fmt.Errorf("no shinoweb api key saved, create one on the site and add it with k on the donation screen")
	}
	baseURL := library.DefaultShinoWebURL
	fmt.Println("uploading donation")
	id, err := dump.UploadDonation(ctx, dump.UploadConfig{
		BaseURL: baseURL, APIKey: apiKey, FilePath: summary.Archive,
	}, func(done, total int) {
		fmt.Printf("part %d/%d\n", done, total)
	})
	if err != nil {
		return err
	}
	fmt.Println("donation", id, "uploaded, waiting for verification on the site")
	return nil
}
