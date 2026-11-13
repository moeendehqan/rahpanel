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
	Text string `json:"text"`
	Data string `json:"callback_data"`
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
	if len(buttons) > 0 {
		p["reply_markup"] = params{"inline_keyboard": buttons}
	}
	if err := b.call(ctx, "sendMessage", p, nil); err != nil && ctx.Err() == nil {
		log.Print(err)
	}
}

func (b Bot) showUser(ctx context.Context, id int64) {
	u, err := b.Store.BotUser(ctx, id)
	if err != nil {
		b.send(ctx, id, "دریافت وضعیت اشتراک ممکن نشد. دوباره تلاش کنید.")
		return
	}
	if u.ID == "" {
		b.send(ctx, id, "اشتراکی به حساب تلگرام شما متصل نیست.")
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
	b.send(ctx, id, fmt.Sprintf("وضعیت: %s\nانقضا: %s\nحجم باقی‌مانده: %s\nلینک اشتراک:\n%s/sub/%s", state,
		u.ExpiresAt.In(time.FixedZone("IRST", 12600)).Format("2006-01-02 15:04"), remaining, strings.TrimRight(b.PublicURL, "/"), u.Token))
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
		price = fmt.Sprintf("%d Stars", p.Stars)
	}
	return fmt.Sprintf("%s · %d روز · %s · %s", p.Name, p.Days, volume(p.QuotaBytes, true), price)
}

func (b Bot) list(ctx context.Context, id int64) {
	u, err := b.Store.BotUser(ctx, id)
	if err != nil {
		b.send(ctx, id, "دریافت اشتراک ممکن نشد. دوباره تلاش کنید.")
		return
	}
	if u.ID != "" && !u.Enabled {
		b.send(ctx, id, "اشتراک غیرفعال است. با مدیر تماس بگیرید.")
		return
	}
	plans, err := b.Store.BotPlans(ctx)
	if err != nil {
		b.send(ctx, id, "دریافت پلن‌ها ممکن نشد. دوباره تلاش کنید.")
		return
	}
	var buttons [][]button
	for _, p := range plans {
		if p.Enabled && (u.ID == "" || (u.GroupID == p.GroupID && (p.Stars > 0 || !u.Active(time.Now())))) {
			buttons = append(buttons, []button{{offer(p), "plan:" + p.ID}})
		}
	}
	if len(buttons) == 0 {
		b.send(ctx, id, "پلن قابل انتخابی برای حساب شما وجود ندارد.")
		return
	}
	for len(buttons) > 0 {
		n := min(len(buttons), 20)
		b.send(ctx, id, "پلن مورد نظر را انتخاب کنید.", buttons[:n]...)
		buttons = buttons[n:]
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
		if err != nil {
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
		case "list":
			b.list(ctx, id)
		case "mine":
			b.showUser(ctx, id)
		case "plan":
			order, err := b.Store.PrepareBotOrder(ctx, id, arg)
			if err != nil {
				b.send(ctx, id, "این پلن در حال حاضر برای حساب شما قابل دریافت نیست.")
				return nil
			}
			label, action := "تأیید دریافت رایگان", "free:"
			if order.Plan.Stars > 0 {
				label, action = "پذیرش شرایط و دریافت فاکتور", "invoice:"
			}
			text := offer(order.Plan)
			if order.Plan.Stars > 0 {
				text += "\n\nشرایط خرید\n" + b.Terms
			}
			b.send(ctx, id, text, []button{{label, action + order.ID}})
		case "free", "invoice":
			result, err := b.Store.ProcessBotOrder(ctx, arg, id, action, "", 0, "")
			if err != nil {
				b.send(ctx, id, "درخواست انجام نشد. وضعیت اشتراک را بررسی کنید و دوباره تلاش کنید.")
				return nil
			}
			if action == "free" {
				b.showUser(ctx, id)
				return nil
			}
			amount, _ := strconv.Atoi(result)
			if err = b.call(ctx, "sendInvoice", params{"chat_id": id, "title": "اشتراک VPN", "description": "خرید یا تمدید اشتراک VPN", "payload": arg,
				"currency": "XTR", "start_parameter": "buy", "prices": []params{{"label": "اشتراک VPN", "amount": amount}}}, nil); err != nil {
				b.send(ctx, id, "ارسال فاکتور ممکن نشد. دوباره دکمه تأیید را بزنید.")
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
			b.send(ctx, id, "پرداخت نیاز به بررسی دارد. از /paysupport استفاده کنید.")
		case err != nil:
			return errors.New("could not store Telegram payment")
		case status == "review":
			b.send(ctx, id, "پرداخت ثبت شد و نیاز به بررسی مدیر دارد. از /paysupport استفاده کنید.")
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
	switch cmd {
	case "/paysupport", "/support":
		b.send(ctx, id, "پشتیبانی خرید\n"+b.Support)
	case "/terms":
		b.send(ctx, id, b.Terms)
	case "/link", "/start":
		if len(parts) == 2 && (cmd == "/link" || strings.HasPrefix(parts[1], "link_")) {
			code := strings.TrimPrefix(parts[1], "link_")
			if err := b.Store.RedeemBotCode(ctx, id, code); err != nil {
				b.send(ctx, id, "کد اتصال نامعتبر یا منقضی است، یا اشتراک قبلاً متصل شده است.")
				return nil
			}
			b.send(ctx, id, "اشتراک به حساب تلگرام شما متصل شد.")
			b.showUser(ctx, id)
		}
		fallthrough
	default:
		b.send(ctx, id, "برای اتصال اشتراک فعلی، کد مدیر را با دستور ‎/link‎ بفرستید.", []button{{"خرید یا تمدید", "list"}, {"اشتراک من", "mine"}})
	}
	return nil
}
