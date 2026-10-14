package cli

import "testing"

// Синк скилла существует ровно ради одного: не потерять правку руками и при
// этом не оставить агента на устаревшей инструкции. Решение принимается по трём
// хешам, и ошибиться тут дорого — поэтому проверяем все ветки.
func TestDecideSkill(t *testing.T) {
	const (
		ours    = "aaa" // то, что мы выложили в прошлый раз
		fresh   = "bbb" // то, что скачали сейчас
		edited  = "ccc" // файл, который правили руками
		unknown = ""    // файла нет
	)
	cases := []struct {
		name          string
		cur, old, new string
		want          skillAction
	}{
		{"файла нет — ставим", unknown, ours, fresh, skillInstall},
		{"уже эта версия", fresh, ours, fresh, skillCurrent},
		{"не правили — перезаписываем", ours, ours, fresh, skillInstall},
		{"правили руками — бэкапим и ставим свежую", edited, ours, fresh, skillInstallBackup},
		{"правили, но версия та же", fresh, ours, fresh, skillCurrent},
		{"хеша ещё нет, файл чужой — бэкапим", edited, unknown, fresh, skillInstallBackup},
	}
	for _, c := range cases {
		if got := decideSkill(c.cur, c.old, c.new); got != c.want {
			t.Errorf("%s: decideSkill(%q, %q, %q) = %v, ждали %v",
				c.name, c.cur, c.old, c.new, got, c.want)
		}
	}
}
