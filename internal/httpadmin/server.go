package httpadmin

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
	
	"shop-bot/internal/bot/messages"
	"shop-bot/internal/broadcast"
	"shop-bot/internal/config"
	logger "shop-bot/internal/log"
	"shop-bot/internal/metrics"
	payment "shop-bot/internal/payment/epay"
	"shop-bot/internal/store"
)

type Server struct {
	adminToken string
	db         *gorm.DB
	bot        *tgbotapi.BotAPI
	epay       *payment.Client
	config     *config.Config
	broadcast  *broadcast.Service
}

func NewServer(adminToken string, db *gorm.DB) *Server {
	// Load config for payment
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		return &Server{
			adminToken: adminToken,
			db:         db,
		}
	}
	
	// Initialize bot API for sending messages
	var bot *tgbotapi.BotAPI
	if cfg.BotToken != "" {
		bot, err = tgbotapi.NewBotAPI(cfg.BotToken)
		if err != nil {
			logger.Error("Failed to init bot API", "error", err)
		}
	}
	
	// Initialize epay client
	var epayClient *payment.Client
	if cfg.EpayPID != "" && cfg.EpayKey != "" && cfg.EpayGateway != "" {
		epayClient = payment.NewClient(cfg.EpayPID, cfg.EpayKey, cfg.EpayGateway)
	}
	
	// Initialize broadcast service
	var broadcastService *broadcast.Service
	if bot != nil {
		broadcastService = broadcast.NewService(db, bot)
	}
	
	return &Server{
		adminToken: adminToken,
		db:         db,
		bot:        bot,
		epay:       epayClient,
		config:     cfg,
		broadcast:  broadcastService,
	}
}

// NewServerWithApp creates a new server with application reference
func NewServerWithApp(adminToken string, app interface{}) *Server {
	// Use reflection to extract fields from app
	appValue := reflect.ValueOf(app)
	if appValue.Kind() == reflect.Ptr {
		appValue = appValue.Elem()
	}
	
	server := &Server{
		adminToken: adminToken,
	}
	
	// Try to get DB field
	if dbField := appValue.FieldByName("DB"); dbField.IsValid() {
		if db, ok := dbField.Interface().(*gorm.DB); ok {
			server.db = db
		}
	}
	
	// Try to get Config field
	if cfgField := appValue.FieldByName("Config"); cfgField.IsValid() {
		if cfg, ok := cfgField.Interface().(*config.Config); ok {
			server.config = cfg
			
			// Initialize payment client
			if cfg.EpayPID != "" && cfg.EpayKey != "" {
				server.epay = payment.NewClient(cfg.EpayPID, cfg.EpayKey, cfg.EpayGateway)
			}
		}
	}
	
	// Try to get Bot field and extract API
	if botField := appValue.FieldByName("Bot"); botField.IsValid() && !botField.IsNil() {
		if method := botField.MethodByName("GetAPI"); method.IsValid() {
			if results := method.Call(nil); len(results) > 0 {
				if api, ok := results[0].Interface().(*tgbotapi.BotAPI); ok {
					server.bot = api
				}
			}
		}
	}
	
	// Try to get Broadcast field
	if broadcastField := appValue.FieldByName("Broadcast"); broadcastField.IsValid() {
		if bc, ok := broadcastField.Interface().(*broadcast.Service); ok {
			server.broadcast = bc
		}
	}
	
	return server
}

// toInt64 converts interface{} to int64
func toInt64(v interface{}) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case int32:
		return int64(val), nil
	case uint:
		return int64(val), nil
	case uint32:
		return int64(val), nil
	case uint64:
		return int64(val), nil
	case float64:
		return int64(val), nil
	case float32:
		return int64(val), nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

func (s *Server) Router() *gin.Engine {
	r := gin.Default()
	
	// Get currency settings
	_, currencySymbol := store.GetCurrencySettings(s.db, s.config)
	
	// Add template functions BEFORE loading templates
	r.SetFuncMap(template.FuncMap{
		"divf": func(a, b interface{}) float64 {
			af, _ := toFloat64(a)
			bf, _ := toFloat64(b)
			if bf == 0 {
				return 0
			}
			return af / bf
		},
		"addf": func(a, b interface{}) float64 {
			af, _ := toFloat64(a)
			bf, _ := toFloat64(b)
			return af + bf
		},
		"subf": func(a, b interface{}) float64 {
			af, _ := toFloat64(a)
			bf, _ := toFloat64(b)
			return af - bf
		},
		"int": func(a interface{}) int {
			f, _ := toFloat64(a)
			return int(f)
		},
		"seq": func(start, end int) []int {
			var result []int
			for i := start; i <= end; i++ {
				result = append(result, i)
			}
			return result
		},
		"currency": func() string {
			return currencySymbol
		},
		"plus": func(a, b interface{}) int64 {
			ai, _ := toInt64(a)
			bi, _ := toInt64(b)
			return ai + bi
		},
		"minus": func(a, b interface{}) int64 {
			ai, _ := toInt64(a)
			bi, _ := toInt64(b)
			return ai - bi
		},
		"multiply": func(a, b interface{}) int64 {
			ai, _ := toInt64(a)
			bi, _ := toInt64(b)
			return ai * bi
		},
	})
	
	// Load HTML templates AFTER setting functions
	r.LoadHTMLGlob("templates/*")

	// Health check
	r.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	
	// Metrics endpoint
	r.GET("/metrics", func(c *gin.Context) {
		promhttp.HandlerFor(
			prometheus.DefaultGatherer,
			promhttp.HandlerOpts{},
		).ServeHTTP(c.Writer, c.Request)
	})
	
	// Add middleware
	r.Use(s.requestLogger())
	r.Use(ErrorHandlerMiddleware())
	
	// Public routes
	public := r.Group("/")
	{
		// Login page
		public.GET("/login", s.handleLoginPage)
		public.POST("/api/login", s.handleLogin)
		public.POST("/api/logout", s.handleLogout)
		
		// Test endpoint to check products
		public.GET("/test/products", func(c *gin.Context) {
			var products []store.Product
			s.db.Find(&products)
			c.JSON(200, gin.H{
				"count": len(products),
				"products": products,
			})
		})
	}
	
	// Payment routes (no auth required for callbacks)
	payment := r.Group("/payment")
	{
		payment.POST("/epay/notify", s.handleEpayNotify)
		payment.GET("/return", s.handlePaymentReturn)
	}

	// Admin routes with auth
	admin := r.Group("/admin")
	admin.Use(s.authMiddleware())
	{
		// Product management
		admin.GET("/products", s.handleProductList)
		admin.POST("/products", s.handleProductCreate)
		admin.PUT("/products/:id", s.handleProductUpdate)
		admin.DELETE("/products/:id", s.handleProductDelete)
		
		// Inventory management
		admin.GET("/products/:id/codes", s.handleProductCodes)
		admin.POST("/products/:id/codes/upload", s.handleCodesUpload)
		admin.DELETE("/codes/:id", s.handleCodeDelete)
		
		// Order management
		admin.GET("/orders", s.handleOrderList)
		
		// Recharge card management
		admin.GET("/recharge-cards", s.handleRechargeCardList)
		admin.POST("/recharge-cards/generate", s.handleRechargeCardGenerate)
		admin.DELETE("/recharge-cards/:id", s.handleRechargeCardDelete)
		admin.GET("/recharge-cards/:id/usage", s.handleRechargeCardUsage)
		
		// Message template management
		admin.GET("/templates", s.handleTemplateList)
		admin.POST("/templates/:id", s.handleTemplateUpdate)
		
		// System settings
		admin.GET("/settings", s.handleSettingsList)
		admin.POST("/settings", s.handleSettingsUpdate)
		
		// Admin dashboard
		admin.GET("/", s.handleAdminDashboard)
	}

	return r
}

// SetupRoutes sets up routes on an existing router
func (s *Server) SetupRoutes(r *gin.Engine) {
	// Static files (CSS, JS)
	r.Static("/static", "./templates")
	
	// Health check
	r.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Metrics
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Root path - login page (only show if not authenticated)
	r.GET("/", func(c *gin.Context) {
		// Check if user is already authenticated
		token := c.GetHeader("Authorization")
		if token == "Bearer "+s.adminToken {
			c.Redirect(http.StatusFound, "/admin/")
			return
		}
		
		// Check cookie
		cookie, err := c.Cookie("admin_token")
		if err == nil && cookie == s.adminToken {
			c.Redirect(http.StatusFound, "/admin/")
			return
		}
		
		// Show login page
		s.handleLoginPage(c)
	})
	
	// API routes
	r.POST("/api/login", s.handleLogin)
	r.POST("/api/logout", s.handleLogout)

	// Payment webhook routes
	r.POST("/payment/epay/notify", s.handleEpayNotify)
	r.GET("/payment/return", s.handlePaymentReturn)
	
	// Test bot endpoint (protected)
	r.POST("/admin/test-bot/:user_id", s.authMiddleware(), s.handleTestBot)

	// Admin routes (protected)
	adminGroup := r.Group("/admin", s.authMiddleware())
	{
		// Product management
		adminGroup.GET("/products", s.handleProductList)
		adminGroup.GET("/products/test", func(c *gin.Context) {
			c.HTML(http.StatusOK, "product_test.html", nil)
		})
		adminGroup.POST("/products", s.handleProductCreate)
		adminGroup.PUT("/products/:id", s.handleProductUpdate)
		adminGroup.DELETE("/products/:id", s.handleProductDelete)
		adminGroup.GET("/products/:id/codes", s.handleProductCodes)
		adminGroup.POST("/products/:id/codes/upload", s.handleCodesUpload)
		adminGroup.DELETE("/codes/:id", s.handleCodeDelete)
		adminGroup.GET("/products/template", s.handleCodeTemplate)
		adminGroup.GET("/codes/template", s.handleCodeTemplate)

		// Order management
		adminGroup.GET("/orders", s.handleOrderList)
		
		// User management
		adminGroup.GET("/users", s.handleUserList)
		adminGroup.GET("/users/:id", s.handleUserDetail)

		// Recharge card management
		adminGroup.GET("/recharge-cards", s.handleRechargeCardList)
		adminGroup.POST("/recharge-cards/generate", s.handleRechargeCardGenerate)
		adminGroup.DELETE("/recharge-cards/:id", s.handleRechargeCardDelete)
		adminGroup.GET("/recharge-cards/:id/usage", s.handleRechargeCardUsage)

		// Template management
		adminGroup.GET("/templates", s.handleTemplateList)
		adminGroup.POST("/templates/:id", s.handleTemplateUpdate)

		// System settings
		adminGroup.GET("/settings", s.handleSettingsList)
		adminGroup.POST("/settings", s.handleSettingsUpdate)
		
		// FAQ management
		adminGroup.GET("/faq", s.handleFAQList)
		adminGroup.POST("/faq", s.handleFAQCreate)
		adminGroup.PUT("/faq/:id", s.handleFAQUpdate)
		adminGroup.DELETE("/faq/:id", s.handleFAQDelete)
		adminGroup.PUT("/faq/:id/sort", s.handleFAQSort)
		adminGroup.POST("/faq/init", s.handleFAQInit)
		
		// Broadcast management
		adminGroup.GET("/broadcast", s.handleBroadcastList)
		adminGroup.POST("/broadcast", s.handleBroadcastCreate)
		adminGroup.GET("/broadcast/:id", s.handleBroadcastDetail)
		
		// Order maintenance APIs
		adminGroup.POST("/api/settings", s.handleSaveSettings)
		adminGroup.POST("/api/orders/expire", s.handleExpireOrders)
		adminGroup.POST("/api/orders/cleanup", s.handleCleanupOrders)

		// Dashboard
		adminGroup.GET("/", s.handleAdminDashboard)
	}
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 首先检查Authorization header
		token := c.GetHeader("Authorization")
		if token == "Bearer "+s.adminToken {
			c.Next()
			return
		}
		
		// 然后检查cookie
		cookie, err := c.Cookie("admin_token")
		if err == nil && cookie == s.adminToken {
			c.Next()
			return
		}
		
		// 如果是API请求或AJAX请求，返回401
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || 
		   c.GetHeader("X-Requested-With") == "XMLHttpRequest" ||
		   strings.Contains(c.GetHeader("Accept"), "application/json") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		
		// 否则重定向到登录页面
		c.Redirect(http.StatusFound, "/")
		c.Abort()
	}
}

func (s *Server) handleEpayNotify(c *gin.Context) {
	metrics.PaymentCallbacksReceived.Inc()
	
	// Parse form data
	if err := c.Request.ParseForm(); err != nil {
		traceID := c.GetString("trace_id")
		logger.Error("Failed to parse form", "error", err, "trace_id", traceID)
		metrics.PaymentCallbacksFailed.Inc()
		c.String(http.StatusBadRequest, "fail")
		return
	}
	
	params := c.Request.Form
	traceID := c.GetString("trace_id")
	logger.Info("Received payment callback", "params", params, "trace_id", traceID)
	
	// Verify signature
	if s.epay == nil || !s.epay.VerifyNotify(params) {
		logger.Error("Invalid callback signature")
		c.String(http.StatusBadRequest, "fail")
		return
	}
	
	// Parse notification
	notify := payment.ParseNotify(params)
	
	// Check trade status
	if notify.TradeStatus != "TRADE_SUCCESS" {
		logger.Info("Trade not successful", "status", notify.TradeStatus)
		c.String(http.StatusOK, "success")
		return
	}
	
	// Find order by out_trade_no
	var order store.Order
	if err := s.db.Preload("User").Preload("Product").Where("epay_out_trade_no = ?", notify.OutTradeNo).First(&order).Error; err != nil {
		logger.Error("Order not found", "out_trade_no", notify.OutTradeNo, "error", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	
	// Check if already paid (idempotency)
	if order.Status != "pending" {
		logger.Info("Order already processed", "order_id", order.ID, "status", order.Status)
		c.String(http.StatusOK, "success")
		return
	}
	
	// Verify amount
	notifyMoney, _ := strconv.ParseFloat(notify.Money, 64)
	if int(notifyMoney*100) != order.AmountCents {
		logger.Error("Amount mismatch", "expected", order.AmountCents, "received", notifyMoney*100)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	
	// Start transaction to update order and claim code
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Update order status
		now := time.Now()
		updates := map[string]interface{}{
			"status":        "paid",
			"epay_trade_no": notify.TradeNo,
			"paid_at":       &now,
		}
		
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return err
		}
		
		// Track metric
		metrics.OrdersPaid.Inc()
		if order.Product != nil && order.Product.Name != "" {
			metrics.RevenueTotal.WithLabelValues(order.Product.Name).Add(float64(order.AmountCents))
		} else {
			metrics.RevenueTotal.WithLabelValues("deposit").Add(float64(order.AmountCents))
		}
		
		// Check if this is a deposit order
		if order.ProductID == nil {
			// This is a deposit order, add balance to user
			if err := store.AddBalance(tx, order.UserID, order.AmountCents, "deposit", 
				fmt.Sprintf("充值订单 #%d", order.ID), nil, &order.ID); err != nil {
				return err
			}
			
			// Update order status to delivered
			if err := tx.Model(&order).Update("status", "delivered").Error; err != nil {
				return err
			}
			
			// Send success message to user
			go s.sendDepositSuccessMessage(&order)
			
			return nil
		}
		
		// Try to claim a code
		ctx := context.Background()
		code, err := store.ClaimOneCodeTx(ctx, tx, *order.ProductID, order.ID)
		if err != nil {
			if err == store.ErrNoStock {
				// Update status to paid_no_stock
				if err := tx.Model(&order).Update("status", "paid_no_stock").Error; err != nil {
					return err
				}
				
				// Track no stock metric
				metrics.OrdersNoStock.Inc()
				
				// Send alert to admin
				go s.alertAdminNoStock(&order)
				
				// Send message to user about no stock
				go s.sendNoStockMessage(&order)
				
				return nil // Transaction successful, but no stock
			}
			return err
		}
		
		// Update order status to delivered
		if err := tx.Model(&order).Update("status", "delivered").Error; err != nil {
			return err
		}
		
		// Track delivered metric
		metrics.OrdersDelivered.Inc()
		
		// Send code to user
		go s.sendCodeToUser(&order, code)
		
		return nil
	})
	
	if err != nil {
		logger.Error("Failed to process payment", "error", err, "order_id", order.ID)
		metrics.PaymentCallbacksFailed.Inc()
		c.String(http.StatusInternalServerError, "fail")
		return
	}
	
	logger.Info("Payment processed successfully", "order_id", order.ID)
	c.String(http.StatusOK, "success")
}

func (s *Server) handlePaymentReturn(c *gin.Context) {
	// Check if this is a payment result with parameters
	tradeStatus := c.Query("trade_status")
	outTradeNo := c.Query("out_trade_no")
	
	if tradeStatus == "TRADE_SUCCESS" && outTradeNo != "" {
		// This looks like a payment notification via GET
		// Convert query params to form values for compatibility
		params := make(url.Values)
		for k, v := range c.Request.URL.Query() {
			params[k] = v
		}
		
		logger.Info("Processing payment return as notification", "out_trade_no", outTradeNo, "params", params)
		
		// Process as payment notification
		s.processPaymentNotification(c, params)
		
		// Show success page
		c.String(http.StatusOK, "Payment completed successfully! Please check your Telegram for the delivery.")
		return
	}
	
	// Simple return page
	c.String(http.StatusOK, "Payment completed. Please check your Telegram for the delivery.")
}

func (s *Server) processPaymentNotification(c *gin.Context, params url.Values) {
	metrics.PaymentCallbacksReceived.Inc()
	
	traceID := c.GetString("trace_id")
	logger.Info("Processing payment notification", "params", params, "trace_id", traceID)
	
	// Verify signature
	if s.epay == nil || !s.epay.VerifyNotify(params) {
		logger.Error("Invalid callback signature", "params", params)
		return
	}
	
	// Parse notification
	notify := payment.ParseNotify(params)
	
	// Check trade status
	if notify.TradeStatus != "TRADE_SUCCESS" {
		logger.Info("Trade not successful", "status", notify.TradeStatus)
		return
	}
	
	// Find order by out_trade_no
	var order store.Order
	if err := s.db.Preload("User").Preload("Product").Where("epay_out_trade_no = ?", notify.OutTradeNo).First(&order).Error; err != nil {
		logger.Error("Order not found", "out_trade_no", notify.OutTradeNo, "error", err)
		return
	}
	
	// Check if already paid (idempotency)
	if order.Status != "pending" {
		logger.Info("Order already processed", "order_id", order.ID, "status", order.Status)
		return
	}
	
	// Verify amount
	notifyMoney, _ := strconv.ParseFloat(notify.Money, 64)
	if int(notifyMoney*100) != order.PaymentAmount {
		logger.Error("Amount mismatch", "expected", order.PaymentAmount, "received", notifyMoney*100)
		return
	}
	
	// Start transaction to update order and claim code
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Update order status
		now := time.Now()
		updates := map[string]interface{}{
			"status":        "paid",
			"epay_trade_no": notify.TradeNo,
			"paid_at":       &now,
		}
		
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return err
		}
		
		// Track metric
		metrics.OrdersPaid.Inc()
		if order.Product != nil && order.Product.Name != "" {
			metrics.RevenueTotal.WithLabelValues(order.Product.Name).Add(float64(order.AmountCents))
		} else {
			metrics.RevenueTotal.WithLabelValues("deposit").Add(float64(order.AmountCents))
		}
		
		// Check if this is a deposit order
		if order.ProductID == nil {
			// This is a deposit order, add balance to user
			if err := store.AddBalance(tx, order.UserID, order.AmountCents, "deposit", 
				fmt.Sprintf("充值订单 #%d", order.ID), nil, &order.ID); err != nil {
				return err
			}
			
			// Update order status to deposit
			if err := tx.Model(&order).Update("status", "deposit").Error; err != nil {
				return err
			}
			
			// Send success message to user
			go s.sendDepositSuccessMessage(&order)
			
			return nil
		}
		
		// Try to claim a code
		ctx := context.Background()
		code, err := store.ClaimOneCodeTx(ctx, tx, *order.ProductID, order.ID)
		if err != nil {
			if err == store.ErrNoStock {
				// Update status to paid_no_stock
				if err := tx.Model(&order).Update("status", "paid_no_stock").Error; err != nil {
					return err
				}
				
				// Track no stock metric
				metrics.OrdersNoStock.Inc()
				
				// Send alert to admin
				go s.alertAdminNoStock(&order)
				
				// Send message to user about no stock
				go s.sendNoStockMessage(&order)
				
				return nil // Transaction successful, but no stock
			}
			return err
		}
		
		// Update order status to delivered
		deliveredAt := time.Now()
		if err := tx.Model(&order).Updates(map[string]interface{}{
			"status": "delivered",
			"delivered_at": &deliveredAt,
		}).Error; err != nil {
			return err
		}
		
		// Track delivered metric
		metrics.OrdersDelivered.Inc()
		
		// Send code to user
		go s.sendCodeToUser(&order, code)
		
		return nil
	})
	
	if err != nil {
		logger.Error("Failed to process payment", "error", err, "order_id", order.ID)
		metrics.PaymentCallbacksFailed.Inc()
		return
	}
	
	logger.Info("Payment processed successfully", "order_id", order.ID)
}

func (s *Server) sendCodeToUser(order *store.Order, code string) {
	if s.bot == nil {
		logger.Error("Bot not initialized, cannot send code")
		return
	}
	
	// Log bot info for debugging
	logger.Info("Bot info", "bot_username", s.bot.Self.UserName, "bot_id", s.bot.Self.ID)
	
	// Get user language
	lang := messages.GetUserLanguage(order.User.Language, "")
	msgManager := messages.GetManager()
	
	// Get product name, handling nil product (e.g., deposit orders)
	productName := "Unknown Product"
	if order.Product != nil {
		productName = order.Product.Name
	}
	
	// Try to get message from template
	templateKey := "order_paid_msg"
	message := msgManager.Get(lang, templateKey)
	
	// If template key not found (returns the key itself), use default message
	if message == templateKey {
		// Fall back to a direct message format
		message = fmt.Sprintf(
			"🎉 支付成功！\n\n订单号：%d\n产品：%s\n卡密：`%s`\n\n感谢您的购买！",
			order.ID, productName, code,
		)
		if lang == "en" {
			message = fmt.Sprintf(
				"🎉 Payment successful!\n\nOrder ID: %d\nProduct: %s\nCode: `%s`\n\nThank you for your purchase!",
				order.ID, productName, code,
			)
		}
	} else {
		// Format the template message
		message = msgManager.Format(lang, templateKey, map[string]interface{}{
			"OrderID":     order.ID,
			"ProductName": productName,
			"Code":        code,
		})
	}
	
	logger.Info("Attempting to send message", "user_id", order.User.TgUserID, "message_preview", message[:50])
	
	msg := tgbotapi.NewMessage(order.User.TgUserID, message)
	msg.ParseMode = "Markdown"
	
	// Send message and log detailed error if fails
	resp, err := s.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send code to user", "error", err, "user_id", order.User.TgUserID, "order_id", order.ID, "error_type", fmt.Sprintf("%T", err))
		// Check if it's an API error with more details
		if apiErr, ok := err.(*tgbotapi.Error); ok {
			logger.Error("Telegram API error details", "code", apiErr.Code, "message", apiErr.Message, "response_params", apiErr.ResponseParameters)
		}
	} else {
		logger.Info("Code sent to user successfully", "order_id", order.ID, "user_id", order.User.TgUserID, "message_id", resp.MessageID, "chat_id", resp.Chat.ID)
	}
}

func (s *Server) sendNoStockMessage(order *store.Order) {
	if s.bot == nil {
		return
	}
	
	// Get user language
	lang := messages.GetUserLanguage(order.User.Language, "")
	msgManager := messages.GetManager()
	
	message := msgManager.Format(lang, "paid_no_stock_msg", map[string]interface{}{
		"OrderID":     order.ID,
		"ProductName": order.Product.Name,
	})
	
	msg := tgbotapi.NewMessage(order.User.TgUserID, message)
	s.bot.Send(msg)
}

func (s *Server) alertAdminNoStock(order *store.Order) {
	productName := "Unknown"
	if order.Product != nil {
		productName = order.Product.Name
	}
	
	logger.Warn("Product out of stock after payment", 
		"order_id", order.ID, 
		"product_id", order.ProductID,
		"product_name", productName,
	)
	// TODO: Send notification to admin users
}

// TestCallbackParams generates test callback parameters
func TestCallbackParams(outTradeNo string, money float64) url.Values {
	params := url.Values{}
	params.Set("pid", "test_pid")
	params.Set("trade_no", fmt.Sprintf("TEST%d", time.Now().Unix()))
	params.Set("out_trade_no", outTradeNo)
	params.Set("type", "alipay")
	params.Set("name", "Test Product")
	params.Set("money", fmt.Sprintf("%.2f", money))
	params.Set("trade_status", "TRADE_SUCCESS")
	
	// Generate test signature (using "test_key" as key)
	// Sort parameters
	var keys []string
	for k := range params {
		if k != "sign" && k != "sign_type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	
	// Build sign string
	var signParts []string
	for _, k := range keys {
		if k != "" && params.Get(k) != "" {
			signParts = append(signParts, fmt.Sprintf("%s=%s", k, params.Get(k)))
		}
	}
	
	signStr := strings.Join(signParts, "&") + "test_key"
	
	// Calculate MD5
	h := md5.New()
	h.Write([]byte(signStr))
	sign := hex.EncodeToString(h.Sum(nil))
	params.Set("sign", sign)
	params.Set("sign_type", "MD5")
	
	return params
}

func (s *Server) sendDepositSuccessMessage(order *store.Order) {
	if s.bot == nil {
		return
	}
	
	user := order.User
	lang := messages.GetUserLanguage(user.Language, "")
	
	// Get new balance
	balance, _ := store.GetUserBalance(s.db, user.ID)
	
	msg := messages.GetManager().Format(lang, "balance_recharged", map[string]interface{}{
		"Amount":      fmt.Sprintf("%.2f", float64(order.AmountCents)/100),
		"NewBalance":  fmt.Sprintf("%.2f", float64(balance)/100),
		"CardCode":    fmt.Sprintf("充值订单#%d", order.ID),
	})
	
	message := tgbotapi.NewMessage(user.TgUserID, msg)
	message.ParseMode = "Markdown"
	
	if _, err := s.bot.Send(message); err != nil {
		logger.Error("Failed to send deposit success message", "error", err, "user_id", user.ID)
	}
}

// handleLoginPage serves the login page
func (s *Server) handleLoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", nil)
}

// handleLogin processes login request
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	
	// Verify token
	if req.Token != s.adminToken {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	
	// Set cookie
	c.SetCookie("admin_token", s.adminToken, 86400*7, "/", "", false, true) // 7 days
	
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleLogout processes logout request
func (s *Server) handleLogout(c *gin.Context) {
	// Clear cookie
	c.SetCookie("admin_token", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleTestBot tests sending a message to a user
func (s *Server) handleTestBot(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	
	if s.bot == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Bot not initialized"})
		return
	}
	
	// Log bot info
	logger.Info("Test bot", "bot_username", s.bot.Self.UserName, "bot_id", s.bot.Self.ID, "target_user", userID)
	
	// Send test message
	testMsg := "🔔 测试消息 / Test Message\n\n这是一条测试消息，用于验证机器人连接。\nThis is a test message to verify bot connection."
	msg := tgbotapi.NewMessage(userID, testMsg)
	msg.ParseMode = "Markdown"
	
	resp, err := s.bot.Send(msg)
	if err != nil {
		logger.Error("Failed to send test message", "error", err, "user_id", userID, "error_type", fmt.Sprintf("%T", err))
		if apiErr, ok := err.(*tgbotapi.Error); ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Failed to send message",
				"telegram_error": apiErr.Message,
				"telegram_code": apiErr.Code,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message_id": resp.MessageID,
		"chat_id": resp.Chat.ID,
		"bot_username": s.bot.Self.UserName,
	})
}
