package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/Fugguri/tgvault/internal/update"
)

// Update проверяет и ставит свежий релиз tgvault с GitHub.
// checkOnly — только показать, force — переустановить, даже если версия та же.
func Update(ctx context.Context, version string, checkOnly, force bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	rel, err := update.Fetch(ctx, update.DefaultRepo, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	cur := update.Normalize(version)
	if !force && !update.Newer(rel.Tag, version) {
		fmt.Printf("✓ уже последняя версия (%s)\n", cur)
		return nil
	}
	fmt.Printf("доступна версия %s (у тебя %s)\n", rel.Tag, cur)
	if checkOnly {
		fmt.Printf("обновиться: tgvault update\n%s\n", rel.Page)
		return nil
	}
	if err := update.Install(ctx, rel, exe); err != nil {
		return err
	}
	fmt.Printf("✓ обновлено: %s\n%s\n", exe, rel.Page)
	return nil
}
