package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/MichiTrader/ishakat/internal/app"
	"github.com/MichiTrader/ishakat/internal/config"
	"github.com/MichiTrader/ishakat/internal/netfix"
	"github.com/MichiTrader/ishakat/internal/xdg"
)

var version = "dev"

func main() {
	_ = netfix.Install()

	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		switch os.Args[1] {
		case "config":
			os.Exit(cmdConfig(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor())
		case "version", "--version", "-v":
			fmt.Println("ishakat", version)
			return
		case "models":
			fmt.Fprintln(os.Stderr, "aún no: paso 6")
			os.Exit(1)
		}
	}

	os.Exit(app.Run(version))
}

func cmdConfig(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "uso: ishakat config <init|path|check> [flags]")
		return 2
	}

	switch args[0] {
	case "path":
		fmt.Println(xdg.ConfigFile())
		return 0

	case "init":
		fs := flag.NewFlagSet("config init", flag.ExitOnError)
		force := fs.Bool("force", false, "sobrescribe la configuración si ya existe")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if err := xdg.EnsureDir(xdg.ConfigDir()); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error creando directorio de configuración: %v\n", err)
			return 1
		}

		if _, err := os.Stat(path); err == nil && !*force {
			fmt.Fprintf(os.Stderr, "✗ El archivo %s ya existe. Usa --force para sobrescribirlo.\n", path)
			return 1
		}

		content := config.ExampleTOML
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error al escribir %s: %v\n", path, err)
			return 1
		}
		fmt.Printf("✓ Configuración inicial creada en: %s (0600)\n", path)
		return 0

	case "check":
		fs := flag.NewFlagSet("config check", flag.ExitOnError)
		strict := fs.Bool("strict", false, "trata las advertencias como errores")
		_ = fs.Parse(args[1:])

		path := xdg.ConfigFile()
		if len(fs.Args()) > 0 {
			path = fs.Arg(0)
		}

		cfg, err := config.Load(config.Options{UserPath: path, SkipProject: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ Error de configuración: %v\n", err)
			return 1
		}

		fmt.Printf("✓ Configuración válida (%d proveedor(es) cargado(s))\n", len(cfg.Providers))
		if len(cfg.Files) > 0 {
			fmt.Println("  Capas leídas:", strings.Join(cfg.Files, ", "))
		}

		if len(cfg.Warnings) > 0 {
			fmt.Printf("  %d advertencia(s):\n", len(cfg.Warnings))
			for _, w := range cfg.Warnings {
				fmt.Printf("    - [%s] %s\n", w.Where, w.Msg)
			}
			if *strict {
				fmt.Fprintln(os.Stderr, "✗ Fallo por flag --strict")
				return 1
			}
		}
		return 0

	default:
		fmt.Fprintf(os.Stderr, "subcomando desconocido: ishakat config %s\n", args[0])
		return 2
	}
}

func cmdDoctor() int {
	rep := netfix.Install()

	fmt.Printf("ishakat %s · doctor\n\n", version)
	fmt.Printf("  go           %s\n", runtime.Version())
	fmt.Printf("  plataforma   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("  cgo          %v\n", netfix.CGOEnabled)
	fmt.Printf("  android      %v\n", rep.Android)
	fmt.Printf("  termux       %v\n", xdg.IsTermux())
	fmt.Printf("  resolv.conf  %v\n", rep.ResolvConf)
	fmt.Printf("  resolver     %s\n", rep.Resolver())
	if len(rep.Servers) > 0 {
		fmt.Printf("  dns          %s  (%s)\n", strings.Join(rep.Servers, ", "), rep.Source)
	}
	fmt.Println()
	fmt.Printf("  config path  %s\n", xdg.ConfigFile())
	fmt.Printf("  cache dir    %s\n", xdg.CacheDir())
	fmt.Printf("  data dir     %s\n", xdg.DataDir())
	fmt.Printf("  state dir    %s\n", xdg.StateDir())
	fmt.Println()

	fmt.Print("  probando DNS (models.dev)... ")
	ips, err := net.LookupHost("models.dev")
	if err != nil {
		fmt.Printf("FALLÓ: %v\n", err)
	} else {
		fmt.Printf("OK (%s)\n", strings.Join(ips, ", "))
	}

	return 0
}
