package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rahpanel/rahpanel/internal/store"
)

type Bot struct {
	Store                            *store.Store
	Token, PublicURL, Terms, Support string
	Test                             bool
}

type payment struct {
	Currency string `json:"currency"`
	Total    int    `json:"total_amount"`
	Order    string `json:"invoice_payload"`
	Charge   string `json:"telegram_payment_charge_id"`
}

type person struct {
	ID int64 `json:"id"`
}
type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type message struct {
	Chat    chat     `json:"chat"`
	From    person   `json:"from"`
	Text    string   `json:"text"`
	Payment *payment `json:"successful_payment"`
}
type update struct {
	ID       int64    `json:"update_id"`
	Message  *message `json:"message"`
	Callback *struct {
		ID      string   `json:"id"`
		From    person   `json:"from"`
		Message *message `json:"message"`
		Data    string   `json:"data"`
	} `json:"callback_query"`
	Checkout *struct {
		payment
		ID   string `json:"id"`
		From person `json:"from"`
	} `json:"pre_checkout_query"`
}
type button struct {
	Text     string            `json:"text"`
	Data     string            `json:"callback_data,omitempty"`
	URL      string            `json:"url,omitempty"`
	CopyText map[string]string `json:"copy_text,omitempty"`
}

var homeRow = []button{{Text: "🏠 منوی اصلی", Data: "home"}}
var plansRow = []button{{Text: "مشاهده پلن‌ها", Data: "list"}}
var supportRow = []button{{Text: "پشتیبانی", Data: "support"}}

func copyRow(label, value string) []button {
	if len([]rune(value)) > 256 {
		return nil
	}
	return []button{{Text: label, CopyText: map[string]string{"text": value}}}
}

type params map[string]any

var client = &http.Client{Timeout: 40 * time.Second}

func (b Bot) call(ctx context.Context, method string, input params, output any) error {
	if method != "getUpdates" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	path := "/"
	if b.Test {
		path = "/test/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+b.Token+path+method, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	var result struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil || !result.OK {
		return fmt.Errorf("Telegram %s failed (HTTP %d)", method, response.StatusCode)
	}
	if output != nil {
		return json.Unmarshal(result.Result, output)
	}
	return nil
}

func (b Bot) Run(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		var updates []update
		err := b.call(ctx, "getUpdates", params{"offset": offset, "timeout": 25, "limit": 10,
			"allowed_updates": []string{"message", "callback_query", "pre_checkout_query"}}, &updates)
		if err == nil {
			for _, u := range updates {
				if u.Checkout != nil {
					_ = b.handle(ctx, u)
				}
			}
			for _, u := range updates {
				if u.Checkout == nil {
					err = b.handle(ctx, u)
					if err != nil {
						break
					}
				}
				offset = u.ID + 1
			}
		}
		if err != nil && ctx.Err() == nil {
			log.Print("Telegram bot will retry: ", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (b Bot) send(ctx context.Context, id int64, text string, buttons ...[]button) {
	p := params{"chat_id": id, "text": text, "link_preview_options": params{"is_disabled": true}}
	var keyboard [][]button
	for _, row := range buttons {
		if len(row) > 0 {
			keyboard = append(keyboard, row)
		}
	}
	if len(keyboard) > 0 {
		p["reply_markup"] = params{"inline_keyboard": keyboard}
	}
	if err := b.call(ctx, "sendMessage", p, nil); err != nil && ctx.Err() == nil {
		log.Print(err)
	}
}

func (b Bot) home(ctx context.Context, id int64) {
	b.send(ctx, id, "سلام! 👋\nبرای خرید یا تمدید اشتراک، پلن‌ها را ببینید. اگر از قبل اشتراک دارید، آن را به حساب تلگرام وصل کنید.",
		[]button{{Text: "🛒 خرید یا تمدید", Data: "list"}, {Text: "👤 اشتراک من", Data: "mine"}},
		[]button{{Text: "🔗 اتصال اشتراک", Data: "link"}, {Text: "🎁 دعوت دوستان", Data: "referrals"}},
		supportRow)
}

func (b Bot) linkHelp(ctx context.Context, id int64) {
	b.send(ctx, id, "🔗 اتصال اشتراک موجود\nاز مدیر کد اتصال بگیرید و دستور دریافتی را همین‌جا بفرستید.\n\nنمونه: /link کد", homeRow)
}

func (b Bot) support(ctx context.Context, id int64) {
	b.send(ctx, id, "💬 پشتیبانی\nبرای پیگیری اشتراک یا پرداخت با این نشانی تماس بگیرید:\n"+b.Support,
		[]button{{Text: "شرایط خرید", Data: "terms"}}, homeRow)
}

func (b Bot) showUser(ctx context.Context, id int64) {
	u, err := b.Store.BotUser(ctx, id)
	if err != nil {
		b.send(ctx, id, "دریافت وضعیت اشتراک ممکن نشد. دوباره تلاش کنید.", []button{{Text: "تلاش دوباره", Data: "mine"}}, homeRow)
		return
	}
	if u.ID == "" {
		b.send(ctx, id, "اشتراکی به حساب تلگرام شما متصل نیست. می‌توانید اشتراک بگیرید یا اشتراک فعلی‌تان را وصل کنید.",
			plansRow, []button{{Text: "اتصال اشتراک موجود", Data: "link"}}, homeRow)
		return
	}
	state := "فعال"
	switch {
	case !u.Enabled:
		state = "غیرفعال؛ با مدیر تماس بگیرید."
	case !u.ExpiresAt.After(time.Now()):
		state = "منقضی"
	case !u.Active(time.Now()):
		state = "حجم تمام شده"
	}
	remaining := "نامحدود"
	if u.QuotaBytes > 0 {
		remaining = volume(max(0, u.QuotaBytes-u.UsedBytes), false)
	}
	link := strings.TrimRight(b.PublicURL, "/") + "/sub/" + u.Token
	b.send(ctx, id, fmt.Sprintf("👤 اشتراک من\n\nوضعیت: %s\nانقضا (به وقت تهران): %s\nحجم باقی‌مانده: %s\n\nلینک اشتراک را در برنامه اتصال خود وارد کنید. این لینک مخصوص حساب شماست.\n%s", state,
		u.ExpiresAt.In(time.FixedZone("IRST", 12600)).Format("2006-01-02 15:04"), remaining, link),
		copyRow("📋 کپی لینک اشتراک", link), []button{{Text: "تمدید اشتراک", Data: "list"}}, homeRow)
}

func volume(n int64, unlimited bool) string {
	if n == 0 && unlimited {
		return "نامحدود"
	}
	if n > 0 && n < 1<<20 {
		return fmt.Sprintf("%d بایت", n)
	}
	if n < 1<<30 {
		return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
}

func offer(p store.BotPlan) string {
	price := "رایگان"
	if p.Stars > 0 {
		price = fmt.Sprintf("%d ⭐", p.Stars)
	}
	return fmt.Sprintf("%s · %d روز · %s · %s", p.Name, p.Days, volume(p.QuotaBytes, true), price)
}

func (b Bot) list(ctx context.Context, id int64) {
	u, err := b.Store.BotUser(ctx, id)
	if err != nil {
		b.send(ctx, id, "دریافت اشتراک ممکن نشد. دوباره تلاش کنید.", plansRow, homeRow)
		return
	}
	if u.ID != "" && !u.Enabled {
		b.send(ctx, id, "اشتراک غیرفعال است. برای بررسی با پشتیبانی تماس بگیرید.", supportRow, homeRow)
		return
	}
	plans, err := b.Store.BotPlans(ctx)
	if err != nil {
		b.send(ctx, id, "دریافت پلن‌ها ممکن نشد. دوباره تلاش کنید.", plansRow, homeRow)
		return
	}
	var buttons [][]button
	for _, p := range plans {
		if p.Enabled && (u.ID == "" || (u.GroupID == p.GroupID && (p.Stars > 0 || !u.Active(time.Now())))) {
			buttons = append(buttons, []button{{Text: offer(p), Data: "plan:" + p.ID}})
		}
	}
	if len(buttons) == 0 {
		b.send(ctx, id, "در حال حاضر پلنی برای حساب شما قابل انتخاب نیست.", supportRow, homeRow)
		return
	}
	for len(buttons) > 0 {
		n := min(len(buttons), 20)
		rows := append(buttons[:n:n], homeRow)
		b.send(ctx, id, "🛒 انتخاب پلن\nمدت، حجم و قیمت روی هر دکمه آمده است.", rows...)
		buttons = buttons[n:]
	}
}

func (b Bot) showOrder(ctx context.Context, id int64, order store.BotOrder) {
	text := "جزئیات سفارش\n\n" + offer(order.Plan)
	rows := [][]button{{{Text: "تأیید دریافت رایگان", Data: "free:" + order.ID}}}
	if order.Plan.Stars > 0 {
		text += fmt.Sprintf("\nهزینه پرداخت: %d ستاره\n\nشرایط خرید\n%s", order.Plan.Stars, b.Terms)
		rows = [][]button{
			{{Text: "پذیرش شرایط و دریافت فاکتور", Data: "invoice:" + order.ID}},
			{{Text: "⭐ راهنمای تهیه ستاره", Data: "stars:" + order.ID}},
		}
	}
	b.send(ctx, id, text, append(rows, []button{{Text: "بازگشت به پلن‌ها", Data: "list"}}, homeRow)...)
}

func (b Bot) resumeOrder(ctx context.Context, id int64, orderID, action string) {
	order, err := b.Store.ReviewBotOrder(ctx, orderID, id)
	switch {
	case errors.Is(err, store.ErrBotOrderStale):
		if action == "fresh" {
			fresh, err := b.Store.PrepareBotOrder(ctx, id, order.Plan.ID)
			if err == nil {
				b.showOrder(ctx, id, fresh)
				return
			}
			b.send(ctx, id, "این پلن در دسترس نیست.", plansRow, homeRow)
			return
		}
		b.send(ctx, id, "مهلت سفارش تمام شده یا مشخصات پلن تغییر کرده است.",
			[]button{{Text: "سفارش تازه", Data: "fresh:" + orderID}}, plansRow, homeRow)
	case errors.Is(err, store.ErrBotPurchaseLimit):
		b.send(ctx, id, "سقف خرید این پلن برای حساب شما تکمیل شده است.", plansRow, homeRow)
	case err != nil:
		b.send(ctx, id, "این سفارش در دسترس نیست.", plansRow, homeRow)
	case order.Status == "fulfilled":
		b.send(ctx, id, "این سفارش تکمیل شده است.", []button{{Text: "اشتراک من", Data: "mine"}}, homeRow)
	case order.Status == "approved" || order.Status == "review":
		b.send(ctx, id, "این سفارش در انتظار نتیجه یا بررسی پرداخت است. برای پیگیری با پشتیبانی تماس بگیرید.", supportRow, homeRow)
	case action == "stars":
		b.send(ctx, id, "⭐ راهنمای تهیه ستاره\n\nبرای شارژ موجودی، در اپ تلگرام به تنظیمات ← My Stars ← Buy Stars بروید یا از @PremiumBot استفاده کنید. بعد به بات برگردید و پرداخت را ادامه دهید.",
			[]button{{Text: "باز کردن @PremiumBot", URL: "https://t.me/PremiumBot"}},
			[]button{{Text: "بازگشت به سفارش", Data: "order:" + orderID}})
	default:
		b.showOrder(ctx, id, order)
	}
}

func (b Bot) handle(ctx context.Context, u update) error {
	if q := u.Checkout; q != nil {
		deadline, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		check, stop := context.WithTimeout(deadline, 3*time.Second)
		_, err := b.Store.ProcessBotOrder(check, q.Order, q.From.ID, "checkout", q.Currency, q.Total, q.ID)
		stop()
		reply := params{"pre_checkout_query_id": q.ID, "ok": err == nil}
		if errors.Is(err, store.ErrBotPurchaseLimit) {
			reply["error_message"] = "سقف خرید این پلن برای حساب شما تکمیل شده است."
		} else if err != nil {
			reply["error_message"] = "سفارش معتبر نیست یا مهلت آن تمام شده است. سفارش تازه‌ای بسازید."
		}
		// An expired checkout query must not block subsequent updates.
		if err = b.call(deadline, "answerPreCheckoutQuery", reply, nil); err != nil {
			log.Print(err)
		}
		return nil
	}
	if q := u.Callback; q != nil {
		if q.Message == nil || q.Message.Chat.Type != "private" || q.Message.Chat.ID != q.From.ID {
			return nil
		}
		id := q.From.ID
		ack, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = b.call(ack, "answerCallbackQuery", params{"callback_query_id": q.ID}, nil)
		cancel()
		action, arg, _ := strings.Cut(q.Data, ":")
		switch action {
		case "home":
			b.home(ctx, id)
		case "link":
			b.linkHelp(ctx, id)
		case "support":
			b.support(ctx, id)
		case "terms":
			b.send(ctx, id, "شرایط خرید\n\n"+b.Terms, homeRow)
		case "list":
			b.list(ctx, id)
		case "mine":
			b.showUser(ctx, id)
		case "referrals":
			b.showReferrals(ctx, id)
		case "stars", "order", "fresh":
			b.resumeOrder(ctx, id, arg, action)
		case "plan":
			order, err := b.Store.PrepareBotOrder(ctx, id, arg)
			if err != nil {
				b.send(ctx, id, "این پلن در حال حاضر برای حساب شما قابل دریافت نیست.", plansRow, homeRow)
				return nil
			}
			b.showOrder(ctx, id, order)
		case "free", "invoice":
			result, err := b.Store.ProcessBotOrder(ctx, arg, id, action, "", 0, "")
			if errors.Is(err, store.ErrBotPurchaseLimit) {
				b.send(ctx, id, "سقف خرید این پلن برای حساب شما تکمیل شده است.", plansRow, homeRow)
				return nil
			}
			if err != nil {
				b.send(ctx, id, "درخواست انجام نشد. پلن را دوباره انتخاب کنید یا با پشتیبانی تماس بگیرید.", plansRow, supportRow, homeRow)
				return nil
			}
			if action == "free" {
				b.showUser(ctx, id)
				return nil
			}
			amount, _ := strconv.Atoi(result)
			if err = b.call(ctx, "sendInvoice", params{"chat_id": id, "title": "اشتراک VPN", "description": "خرید یا تمدید اشتراک VPN", "payload": arg,
				"currency": "XTR", "start_parameter": "buy", "prices": []params{{"label": "اشتراک VPN", "amount": amount}}}, nil); err != nil {
				b.send(ctx, id, "ارسال فاکتور ممکن نشد.", []button{{Text: "ارسال دوباره فاکتور", Data: "invoice:" + arg}}, homeRow)
			}
		}
		return nil
	}
	m := u.Message
	if m == nil || m.Chat.Type != "private" || m.Chat.ID != m.From.ID {
		return nil
	}
	id := m.From.ID
	if p := m.Payment; p != nil {
		status, err := b.Store.ProcessBotOrder(ctx, p.Order, id, "receipt", p.Currency, p.Total, p.Charge)
		switch {
		case errors.Is(err, store.ErrBotUnavailable):
			b.send(ctx, id, "پرداخت نیاز به بررسی دارد. برای پیگیری با پشتیبانی تماس بگیرید.", supportRow, homeRow)
		case err != nil:
			return errors.New("could not store Telegram payment")
		case status == "review":
			b.send(ctx, id, "پرداخت ثبت شد و نیاز به بررسی مدیر دارد.", supportRow, homeRow)
		default:
			b.showUser(ctx, id)
		}
		return nil
	}
	parts := strings.Fields(m.Text)
	if len(parts) == 0 {
		return nil
	}
	cmd, _, _ := strings.Cut(parts[0], "@")
	if cmd == "/start" {
		var code string
		if len(parts) == 2 {
			if arg, ok := strings.CutPrefix(parts[1], "ref_"); ok {
				code = arg
			}
		}
		if err := b.Store.RecordBotStart(ctx, id, code); err != nil {
			return errors.New("could not record Telegram start")
		}
	}
	switch cmd {
	case "/invite":
		b.showReferrals(ctx, id)
	case "/paysupport", "/support":
		b.support(ctx, id)
	case "/terms":
		b.send(ctx, id, "شرایط خرید\n\n"+b.Terms, homeRow)
	case "/link", "/start":
		if len(parts) == 2 && (cmd == "/link" || strings.HasPrefix(parts[1], "link_")) {
			code := strings.TrimPrefix(parts[1], "link_")
			if err := b.Store.RedeemBotCode(ctx, id, code); err != nil {
				b.send(ctx, id, "کد اتصال نامعتبر یا منقضی است، یا اشتراک قبلاً متصل شده است.", []button{{Text: "راهنمای اتصال", Data: "link"}}, homeRow)
				return nil
			}
			b.send(ctx, id, "اشتراک به حساب تلگرام شما متصل شد.")
			b.showUser(ctx, id)
			return nil
		}
		if cmd == "/link" {
			b.linkHelp(ctx, id)
			return nil
		}
		b.home(ctx, id)
	default:
		b.home(ctx, id)
	}
	return nil
}

func (b Bot) showReferrals(ctx context.Context, id int64) {
	cfg, err := b.Store.ReferralSettings(ctx)
	if err != nil {
		b.send(ctx, id, "دریافت تنظیمات دعوت ممکن نشد.", homeRow)
		return
	}
	p, err := b.Store.ReferralProgress(ctx, id)
	if err != nil {
		b.send(ctx, id, "دریافت آمار دعوت ممکن نشد.", homeRow)
		return
	}
	message := fmt.Sprintf("دعوت موفق: %d\nدعوت برای پاداش بعدی: %d از %d\nپاداش در انتظار: %d روز و %d گیبی‌بایت", p.Total, p.Unused, cfg.Threshold, p.PendingDays, p.PendingGiB)
	if !cfg.Enabled {
		b.send(ctx, id, "دعوت دوستان در حال حاضر غیرفعال است.\n\n"+message, homeRow)
		return
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", params{}, &me); err != nil || me.Username == "" {
		b.send(ctx, id, "دریافت لینک دعوت ممکن نشد. دوباره تلاش کنید.\n\n"+message, []button{{Text: "تلاش دوباره", Data: "referrals"}}, homeRow)
		return
	}
	link := fmt.Sprintf("https://t.me/%s?start=ref_%s", me.Username, p.Code)
	b.send(ctx, id, fmt.Sprintf("🎁 دعوت دوستان\n\nلینک را برای دوستانتان بفرستید. وقتی برای اولین بار بات را با این لینک شروع کنند، دعوت شما ثبت می‌شود.\n\n%s\nپاداش هر گروه: %d روز و %d گیبی‌بایت\n\n%s", message, cfg.Days, cfg.GiB, link),
		copyRow("📋 کپی لینک دعوت", link), homeRow)
}
