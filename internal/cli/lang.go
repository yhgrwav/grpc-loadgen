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
	"fmt"
	"os"
	"strconv"
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
	return t.get(phrase{LangRU: "выполняется нагрузочное тестирование", LangEN: "load test running", LangDE: "Lasttest läuft", LangZH: "负载测试进行中"})
}

func (t Text) Stopping() string {
	return t.get(phrase{LangRU: "останавливаюсь", LangEN: "stopping", LangDE: "wird gestoppt", LangZH: "正在停止"})
}

func (t Text) Finished() string {
	return t.get(phrase{LangRU: "тестирование завершено", LangEN: "load test finished", LangDE: "Lasttest beendet", LangZH: "负载测试已完成"})
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

// InFlightCount is the header's count of calls still waiting for an answer;
// n is already formatted.
func (t Text) InFlightCount(n string) string {
	return t.get(phrase{
		LangRU: n + " в полёте",
		LangEN: n + " in flight",
		LangDE: n + " unterwegs",
		LangZH: n + " 在途",
	})
}

// StopAgainAborts says what another press of the stop key does while the
// calls in flight drain.
func (t Text) StopAgainAborts() string {
	return t.get(phrase{
		LangRU: "q ещё раз - оборвать",
		LangEN: "q again cuts them off",
		LangDE: "nochmal q - abbrechen",
		LangZH: "再按 q 立即中断",
	})
}

// StopAgainExits is the way out once the abort is under way: it records the
// results and builds the report, and if that hangs only this leaves.
func (t Text) StopAgainExits() string {
	return t.get(phrase{
		LangRU: "q ещё раз - выйти без отчёта",
		LangEN: "q again exits without a report",
		LangDE: "nochmal q - ohne Bericht beenden",
		LangZH: "再按 q 退出，不输出报告",
	})
}

func (t Text) Target() string {
	return t.get(phrase{LangRU: "цель", LangEN: "target", LangDE: "Ziel", LangZH: "目标"})
}

func (t Text) Latency() string {
	return t.get(phrase{LangRU: "латенси", LangEN: "latency", LangDE: "Latenz", LangZH: "延迟"})
}

// Duration is how long the run went on, as a label for the run itself: the
// same number under "latency" would read as a measurement of the target.
func (t Text) Duration() string {
	return t.get(phrase{LangRU: "длительность", LangEN: "duration", LangDE: "Dauer", LangZH: "时长"})
}

func (t Text) Rate() string {
	return t.get(phrase{LangRU: "частота", LangEN: "rate", LangDE: "Rate", LangZH: "速率"})
}

func (t Text) HintTabs() string {
	return t.get(phrase{LangRU: "<- -> вкладки", LangEN: "<- -> tabs", LangDE: "<- -> Reiter", LangZH: "<- -> 标签页"})
}

func (t Text) HintHelp() string {
	return t.get(phrase{LangRU: "? помощь", LangEN: "? help", LangDE: "? Hilfe", LangZH: "? 帮助"})
}

func (t Text) HintQuit() string {
	return t.get(phrase{LangRU: "q выйти", LangEN: "q quit", LangDE: "q beenden", LangZH: "q 退出"})
}

// UnknownKey is the hint for a key that is no command. It is short enough for
// the narrowest view, and points at the layout: a letter from another layout
// is the usual reason.
func (t Text) UnknownKey(key string) string {
	return t.get(phrase{
		LangRU: `"` + key + `" не команда - проверьте раскладку | ? помощь`,
		LangEN: `"` + key + `" is not a key here - check the layout | ? help`,
		LangDE: `"` + key + `" ist kein Befehl - Layout prüfen | ? Hilfe`,
		LangZH: `"` + key + `" 不是命令 - 请检查键盘布局 | ? 帮助`,
	})
}

// FakeTarget names the built-in fake target in the header.
func (t Text) FakeTarget() string {
	return t.get(phrase{
		LangRU: "заглушка (-fake)",
		LangEN: "fake target (-fake)",
		LangDE: "Attrappe (-fake)",
		LangZH: "模拟目标 (-fake)",
	})
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
		LangRU: "растёт доля ошибок - смотри разбивку на вкладке метода",
		LangEN: "the error share is growing - see the per-method tab",
		LangDE: "der Fehleranteil steigt - siehe den Reiter der Methode",
		LangZH: "错误占比正在上升，请查看对应方法的标签页",
	})
}

func (t Text) HelpTitle() string {
	return t.get(phrase{LangRU: "Управление", LangEN: "Keys", LangDE: "Tasten", LangZH: "按键"})
}

func (t Text) HelpEscape() string {
	return t.get(phrase{
		LangRU: "выйти из настроек, иначе вернуться на сводку",
		LangEN: "leave the settings, otherwise go back to the summary",
		LangDE: "die Einstellungen verlassen, sonst zurück zur Übersicht",
		LangZH: "退出设置，否则返回概览",
	})
}

func (t Text) HelpTabs() string {
	return t.get(phrase{LangRU: "переключить вкладку", LangEN: "switch tab", LangDE: "Reiter wechseln", LangZH: "切换标签页"})
}

func (t Text) HelpHelp() string {
	return t.get(phrase{LangRU: "показать и скрыть эту справку", LangEN: "show and hide this help", LangDE: "diese Hilfe ein- und ausblenden", LangZH: "显示或隐藏此帮助"})
}

func (t Text) HelpQuit() string {
	return t.get(phrase{
		LangRU: "остановить прогон и напечатать отчёт",
		LangEN: "stop the run and print the report",
		LangDE: "Lauf stoppen und Bericht ausgeben",
		LangZH: "停止运行并输出报告",
	})
}

func (t Text) HelpSettings() string {
	return t.get(phrase{
		LangRU: "открыть настройки: язык, тема, палитра",
		LangEN: "open the settings: language, mode, palette",
		LangDE: "Einstellungen öffnen: Sprache, Modus, Palette",
		LangZH: "打开设置：语言、模式、配色",
	})
}

func (t Text) PickLanguage() string {
	return "Язык интерфейса | Interface language | Sprache | 界面语言"
}

func (t Text) Settings() string {
	return t.get(phrase{LangRU: "настройки", LangEN: "settings", LangDE: "Einstellungen", LangZH: "设置"})
}

func (t Text) LanguageRow() string {
	return t.get(phrase{LangRU: "Язык", LangEN: "Language", LangDE: "Sprache", LangZH: "语言"})
}

func (t Text) ModeRow() string {
	return t.get(phrase{LangRU: "Тема", LangEN: "Mode", LangDE: "Modus", LangZH: "模式"})
}

func (t Text) PaletteRow() string {
	return t.get(phrase{LangRU: "Палитра", LangEN: "Palette", LangDE: "Palette", LangZH: "配色"})
}

func (t Text) ModeDark() string {
	return t.get(phrase{LangRU: "тёмная", LangEN: "dark", LangDE: "dunkel", LangZH: "深色"})
}

func (t Text) ModeLight() string {
	return t.get(phrase{LangRU: "светлая", LangEN: "light", LangDE: "hell", LangZH: "浅色"})
}

func (t Text) SettingsHint() string {
	return t.get(phrase{
		LangRU: "up/down строка   <- -> значение   esc к вкладкам   сохраняется сразу",
		LangEN: "up/down row   <- -> value   esc back to the tabs   saved immediately",
		LangDE: "up/down Zeile   <- -> Wert   esc zurück zu den Reitern   sofort gespeichert",
		LangZH: "up/down 选择行   <- -> 切换值   esc 返回标签页   立即保存",
	})
}

func (t Text) SettingsLocked() string {
	return t.get(phrase{
		LangRU: "enter изменить   <- -> вкладки   esc к сводке",
		LangEN: "enter to edit   <- -> tabs   esc to the summary",
		LangDE: "enter zum Ändern   <- -> Reiter   esc zur Übersicht",
		LangZH: "enter 编辑   <- -> 标签页   esc 返回概览",
	})
}

func (t Text) HintBack() string {
	return t.get(phrase{LangRU: "esc назад", LangEN: "esc back", LangDE: "esc zurück", LangZH: "esc 返回"})
}

func (t Text) ReportTitle() string {
	return t.get(phrase{LangRU: "Прогон завершён", LangEN: "Run finished", LangDE: "Lauf beendet", LangZH: "运行结束"})
}

func (t Text) ReportStopped() string {
	return t.get(phrase{LangRU: "Прогон остановлен", LangEN: "Run stopped", LangDE: "Lauf gestoppt", LangZH: "运行已中止"})
}

func (t Text) ReportStoppedNote() string {
	return t.get(phrase{
		LangRU: "прогон прерван, нагрузка была не полной - числа ниже описывают только то, что успело пройти",
		LangEN: "the run was cut short, so the numbers below describe only the part that ran",
		LangDE: "der Lauf wurde abgebrochen; die Zahlen unten beschreiben nur den gelaufenen Teil",
		LangZH: "运行被中止，以下数字仅反映已完成的部分",
	})
}

func (t Text) ColumnMethod() string {
	return t.get(phrase{LangRU: "метод", LangEN: "method", LangDE: "Methode", LangZH: "方法"})
}

func (t Text) PressToExit() string {
	return t.get(phrase{
		LangRU: "enter выйти",
		LangEN: "enter to exit",
		LangDE: "enter zum Beenden",
		LangZH: "enter 退出",
	})
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
		LangRU: "up/down выбрать   enter подтвердить",
		LangEN: "up/down move   enter confirm",
		LangDE: "up/down wählen   enter bestätigen",
		LangZH: "up/down 选择   enter 确认",
	})
}

// WarmupNoteShort is WarmupNote for a narrow frame. It leaves out the time
// left, which the header shows, so its width does not depend on the warmup.
func (t Text) WarmupNoteShort() string {
	return t.get(phrase{
		LangRU: "прогрев: p99 пока пуст",
		LangEN: "warming up: no p99 yet",
		LangDE: "Aufwärmphase: p99 noch leer",
		LangZH: "预热中：p99 暂为空",
	})
}

func (t Text) ErrorsNoteShort() string {
	return t.get(phrase{
		LangRU: "ошибок больше - см. вкладку метода",
		LangEN: "error share growing - see method tab",
		LangDE: "Fehleranteil steigt - siehe Methode",
		LangZH: "错误占比上升，见方法标签页",
	})
}

func (t Text) InFlightNoteShort() string {
	return t.get(phrase{
		LangRU: "запросы копятся: сервис не успевает",
		LangEN: "requests piling up: service slower than send rate",
		LangDE: "Anfragen stauen sich: Dienst zu langsam",
		LangZH: "请求堆积：服务跟不上",
	})
}

// TooNarrow replaces the frame on a terminal narrower than minimum columns.
func (t Text) TooNarrow(minimum int) string {
	n := strconv.Itoa(minimum)

	return t.get(phrase{
		LangRU: "окно уже " + n + " колонок - расширьте",
		LangEN: "window narrower than " + n + " columns - widen it",
		LangDE: "Fenster schmaler als " + n + " Spalten - bitte verbreitern",
		LangZH: "窗口不足 " + n + " 列，请加宽",
	})
}

func (t Text) NotSent() string {
	return t.get(phrase{LangRU: "не отправлено", LangEN: "not sent", LangDE: "nicht gesendet", LangZH: "未发送"})
}

// SentColumn heads the final table's sent column: "отправлено" is too wide
// for a column of counts.
func (t Text) SentColumn() string {
	return t.get(phrase{LangRU: "отпр.", LangEN: "sent", LangDE: "gesendet", LangZH: "已发送"})
}

// MoreMethods counts the methods a short terminal left out of the table.
func (t Text) MoreMethods(n int) string {
	if n == 1 && t.lang == LangEN {
		return "1 more method"
	}

	return t.get(phrase{
		LangRU: fmt.Sprintf("ещё методов: %d", n),
		LangEN: fmt.Sprintf("%d more methods", n),
		LangDE: fmt.Sprintf("%d weitere Methoden", n),
		LangZH: fmt.Sprintf("还有 %d 个方法", n),
	})
}

// MoreLines counts what a short terminal left out of the final screen.
func (t Text) MoreLines(n int) string {
	return t.get(phrase{
		LangRU: fmt.Sprintf("ещё строк: %d, полный отчёт будет напечатан после выхода", n),
		LangEN: fmt.Sprintf("%d more lines, the full report is printed after exit", n),
		LangDE: fmt.Sprintf("%d weitere Zeilen, der vollständige Bericht wird nach dem Beenden ausgegeben", n),
		LangZH: fmt.Sprintf("还有 %d 行，完整报告将在退出后打印", n),
	})
}

// TooShort replaces the final screen on a terminal too short for any of it.
func (t Text) TooShort() string {
	return t.get(phrase{
		LangRU: "терминал слишком мал, полный отчёт будет напечатан после выхода",
		LangEN: "terminal too small, the full report is printed after exit",
		LangDE: "Terminal zu klein, der vollständige Bericht wird nach dem Beenden ausgegeben",
		LangZH: "终端太小，完整报告将在退出后打印",
	})
}

// Failed is the count of failed calls, the text report's "failed".
func (t Text) Failed() string {
	return t.get(phrase{LangRU: "ошибки", LangEN: "failed", LangDE: "Fehler", LangZH: "失败"})
}
