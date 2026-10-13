package approve

import (
	"testing"
	"time"
)

func withTempDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TGVAULT_APPROVE_DIR", dir)
}

func TestIDDeterministic(t *testing.T) {
	a := ID("send", Canon("x", "привет"))
	b := ID("send", Canon("x", "привет"))
	c := ID("send", Canon("x", "привет!"))
	if a != b {
		t.Fatalf("id не детерминирован: %s != %s", a, b)
	}
	if a == c {
		t.Fatal("разное содержимое дало одинаковый id")
	}
	if ID("edit", Canon("x", "привет")) == a {
		t.Fatal("разное действие дало одинаковый id")
	}
}

func TestRequestGrantConsume(t *testing.T) {
	withTempDir(t)
	id := ID("send", Canon("chat:1", "hi"))
	if err := Request(id, "send", "SEND hello"); err != nil {
		t.Fatal(err)
	}
	if err := Consume(id); err == nil {
		t.Fatal("без согласования Consume должен падать")
	}
	p, err := Load(id)
	if err != nil || p.Approved() {
		t.Fatalf("запрос должен быть не согласован: %+v %v", p, err)
	}

	if err := Grant(id, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := Consume(id); err != nil {
		t.Fatalf("после согласования Consume должен пройти: %v", err)
	}
	// одноразовость: повторный Consume — уже нет.
	if err := Consume(id); err == nil {
		t.Fatal("согласование должно расходоваться один раз")
	}
}

func TestExpiry(t *testing.T) {
	withTempDir(t)
	id := ID("send", Canon("chat:1", "hi"))
	_ = Request(id, "send", "x")
	if err := Grant(id, time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := Consume(id); err == nil {
		t.Fatal("истёкшее согласование не должно проходить")
	}
}

func TestContentBinding(t *testing.T) {
	withTempDir(t)
	idOK := ID("send", Canon("chat:1", "согласованный текст"))
	_ = Request(idOK, "send", "x")
	_ = Grant(idOK, time.Minute)

	// другой текст → другой id → согласования на него нет.
	idOther := ID("send", Canon("chat:1", "другой текст"))
	if idOK == idOther {
		t.Fatal("разный текст должен давать разный id")
	}
	if err := Consume(idOther); err == nil {
		t.Fatal("согласование одного текста не должно пропускать другой")
	}
}

func TestList(t *testing.T) {
	withTempDir(t)
	_ = Request(ID("send", Canon("a")), "send", "A")
	_ = Request(ID("send", Canon("b")), "send", "B")
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("ожидалось 2 запроса, got %d", len(list))
	}
}
