// Copyright 2026 yhgrwav
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"os"
	"strings"
)

type Lang string

const (
	LangRU Lang = "ru"
	LangEN Lang = "en"
	LangDE Lang = "de"
	LangZH Lang = "zh"
)

type langOption struct {
	Lang  Lang
	Title string
}

// Languages lists the interface languages the tool ships with.
func Languages() []langOption {
	return []langOption{
		{Lang: LangRU, Title: "Русский"},
		{Lang: LangEN, Title: "English"},
		{Lang: LangDE, Title: "Deutsch"},
		{Lang: LangZH, Title: "中文"},
	}
}

// DetectLang guesses the interface language from the environment.
func DetectLang() Lang {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := strings.ToLower(os.Getenv(name))
		if value == "" {
			continue
		}

		switch {
		case strings.HasPrefix(value, "ru"):
			return LangRU
		case strings.HasPrefix(value, "de"):
			return LangDE
		case strings.HasPrefix(value, "zh"):
			return LangZH
		case strings.HasPrefix(value, "en"):
			return LangEN
		}
	}

	return LangEN
}

type phrase map[Lang]string

func (p phrase) in(lang Lang) string {
	if text, ok := p[lang]; ok {
		return text
	}

	return p[LangEN]
}

// Text holds every string the interface shows, in every language.
type Text struct {
	lang Lang
}

// NewText builds the translator for a language.
func NewText(lang Lang) Text {
	return Text{lang: lang}
}

func (t Text) Lang() Lang { return t.lang }

func (t Text) get(p phrase) string { return p.in(t.lang) }

func (t Text) Summary() string {
	return t.get(phrase{LangRU: "сводка", LangEN: "summary", LangDE: "Übersicht", LangZH: "总览"})
}

func (t Text) Running() string {
	return t.get(phrase{LangRU: "идёт прогон", LangEN: "running", LangDE: "läuft", LangZH: "运行中"})
}

func (t Text) Stopping() string {
	return t.get(phrase{LangRU: "останавливаюсь", LangEN: "stopping", LangDE: "wird gestoppt", LangZH: "正在停止"})
}

func (t Text) Finished() string {
	return t.get(phrase{LangRU: "завершён", LangEN: "finished", LangDE: "beendet", LangZH: "已完成"})
}

func (t Text) Sent() string {
	return t.get(phrase{LangRU: "отправлено", LangEN: "sent", LangDE: "gesendet", LangZH: "已发送"})
}

func (t Text) Errors() string {
	return t.get(phrase{LangRU: "ошибки", LangEN: "errors", LangDE: "Fehler", LangZH: "错误"})
}

func (t Text) InFlight() string {
	return t.get(phrase{LangRU: "в полёте", LangEN: "in flight", LangDE: "unterwegs", LangZH: "在途"})
}

func (t Text) Target() string {
	return t.get(phrase{LangRU: "цель", LangEN: "target", LangDE: "Ziel", LangZH: "目标"})
}

func (t Text) Latency() string {
	return t.get(phrase{LangRU: "латенси", LangEN: "latency", LangDE: "Latenz", LangZH: "延迟"})
}

func (t Text) Rate() string {
	return t.get(phrase{LangRU: "частота", LangEN: "rate", LangDE: "Rate", LangZH: "速率"})
}

func (t Text) HintTabs() string {
	return t.get(phrase{LangRU: "tab вкладки", LangEN: "tab switch", LangDE: "tab wechseln", LangZH: "tab 切换"})
}

func (t Text) HintHelp() string {
	return t.get(phrase{LangRU: "? помощь", LangEN: "? help", LangDE: "? Hilfe", LangZH: "? 帮助"})
}

func (t Text) HintQuit() string {
	return t.get(phrase{LangRU: "q остановить", LangEN: "q stop", LangDE: "q stoppen", LangZH: "q 停止"})
}

func (t Text) HintCommands() string {
	return t.get(phrase{LangRU: "/ команды", LangEN: "/ commands", LangDE: "/ Befehle", LangZH: "/ 命令"})
}

func (t Text) WarmupNote(left string) string {
	return t.get(phrase{
		LangRU: "идёт прогрев (" + left + "): эти запросы не попадут в percentiles, поэтому p99 пока пуст",
		LangEN: "warming up (" + left + "): these requests stay out of the percentiles, so p99 is still empty",
		LangDE: "Aufwärmphase (" + left + "): diese Anfragen bleiben aus den Perzentilen, daher ist p99 noch leer",
		LangZH: "预热中（" + left + "）：这些请求不计入分位数，因此 p99 暂为空",
	})
}

func (t Text) InFlightNote() string {
	return t.get(phrase{
		LangRU: "запросы копятся: сервис отвечает медленнее, чем мы шлём",
		LangEN: "requests are piling up: the service answers slower than we send",
		LangDE: "Anfragen stauen sich: der Dienst antwortet langsamer als gesendet wird",
		LangZH: "请求正在堆积：服务响应慢于发送速度",
	})
}

func (t Text) ErrorsNote() string {
	return t.get(phrase{
		LangRU: "растёт доля ошибок — смотри разбивку на вкладке метода",
		LangEN: "the error share is growing — see the per-method tab",
		LangDE: "der Fehleranteil steigt — siehe den Reiter der Methode",
		LangZH: "错误占比正在上升——请查看对应方法的标签页",
	})
}

func (t Text) HelpTitle() string {
	return t.get(phrase{LangRU: "Управление", LangEN: "Keys", LangDE: "Tasten", LangZH: "按键"})
}

func (t Text) HelpTabs() string {
	return t.get(phrase{LangRU: "переключить вкладку", LangEN: "switch tab", LangDE: "Reiter wechseln", LangZH: "切换标签页"})
}

func (t Text) HelpHelp() string {
	return t.get(phrase{LangRU: "показать и скрыть эту справку", LangEN: "show and hide this help", LangDE: "diese Hilfe ein- und ausblenden", LangZH: "显示或隐藏此帮助"})
}

func (t Text) HelpQuit() string {
	return t.get(phrase{LangRU: "остановить прогон и напечатать отчёт", LangEN: "stop the run and print the report", LangDE: "Lauf stoppen und Bericht ausgeben", LangZH: "停止运行并输出报告"})
}

func (t Text) HelpCommands() string {
	return t.get(phrase{LangRU: "команды: /lang, /theme, /help, /quit", LangEN: "commands: /lang, /theme, /help, /quit", LangDE: "Befehle: /lang, /theme, /help, /quit", LangZH: "命令：/lang、/theme、/help、/quit"})
}

func (t Text) PickLanguage() string {
	return "Язык интерфейса · Interface language · Sprache · 界面语言"
}

func (t Text) PickTheme() string {
	return t.get(phrase{LangRU: "Тема оформления", LangEN: "Colour theme", LangDE: "Farbschema", LangZH: "配色主题"})
}

func (t Text) Saved(path string) string {
	return t.get(phrase{
		LangRU: "настройки сохранены в " + path,
		LangEN: "settings saved to " + path,
		LangDE: "Einstellungen gespeichert in " + path,
		LangZH: "设置已保存至 " + path,
	})
}

func (t Text) PickHint() string {
	return t.get(phrase{
		LangRU: "↑↓ выбрать   enter подтвердить",
		LangEN: "↑↓ move   enter confirm",
		LangDE: "↑↓ wählen   enter bestätigen",
		LangZH: "↑↓ 选择   enter 确认",
	})
}

func (t Text) UnknownCommand(name string) string {
	return t.get(phrase{
		LangRU: "неизвестная команда: " + name,
		LangEN: "unknown command: " + name,
		LangDE: "unbekannter Befehl: " + name,
		LangZH: "未知命令：" + name,
	})
}
