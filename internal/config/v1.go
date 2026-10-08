package config

import "encoding/json"

// V1 — старый формат конфига (.tg-import.json без "version").
type V1 struct {
	Out      string   `json:"out"`
	FilesDir string   `json:"files_dir"`
	DB       string   `json:"db"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Chats    []V1Chat `json:"chats"`
}

// V1Chat — запись чата в старом формате.
type V1Chat struct {
	Name        string `json:"name"`
	ID          int64  `json:"id"`
	Alias       string `json:"alias"`
	Slug        string `json:"slug"`
	TopicID     int    `json:"topic_id"`
	Topic       int    `json:"topic"`
	IncludeBots bool   `json:"include_bots"`
	FilesDir    string `json:"files_dir"`
	DB          string `json:"db"`
}

// IsV1 определяет старый формат: есть "chats" и нет "version".
func IsV1(raw []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	if _, hasVersion := probe["version"]; hasVersion {
		return false
	}
	_, hasChats := probe["chats"]
	return hasChats
}

// ParseV1 разбирает старый конфиг.
func ParseV1(raw []byte) (*V1, error) {
	var v V1
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// TopicIDOf возвращает topic_id из старой записи (поддерживает оба поля).
func (c V1Chat) TopicIDOf() int {
	if c.TopicID != 0 {
		return c.TopicID
	}
	return c.Topic
}
