package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.bug.st/serial"
	_ "modernc.org/sqlite"
)

type WeightReading struct {
	IsStable bool    `json:"is_stable"`
	Unit     string  `json:"unit"`
	Value    float64 `json:"value"`
	Raw      string  `json:"raw"`
}

type TicketRequest struct {
	Placa     string  `json:"placa"`
	Chofer    string  `json:"chofer"`
	Producto  string  `json:"producto"`
	PesoBruto float64 `json:"peso_bruto"`
	Tara      float64 `json:"tara"`
	Unidad    string  `json:"unidad"`
}

type TicketResponse struct {
	ID       int64   `json:"id"`
	Fecha    string  `json:"fecha"`
	PesoNeto float64 `json:"peso_neto"`
	Success  bool    `json:"success"`
}

type TicketRecord struct {
	ID        int64   `json:"id"`
	Fecha     string  `json:"fecha"`
	Placa     string  `json:"placa"`
	Chofer    string  `json:"chofer"`
	Producto  string  `json:"producto"`
	PesoBruto float64 `json:"peso_bruto"`
	Tara      float64 `json:"tara"`
	PesoNeto  float64 `json:"peso_neto"`
	Unidad    string  `json:"unidad"`
}

type PortConfigRequest struct {
	Port string `json:"port"`
}

type FirstWeighRequest struct {
	Placa       string  `json:"placa"`
	Chofer      string  `json:"chofer"`
	Producto    string  `json:"producto"`
	PesoEntrada float64 `json:"peso_entrada"`
	Unidad      string  `json:"unidad"`
}

type SecondWeighRequest struct {
	TicketID   int64   `json:"ticket_id"`
	PesoSalida float64 `json:"peso_salida"`
}

type TicketV2Record struct {
	ID           int64    `json:"id"`
	FechaEntrada string   `json:"fecha_entrada"`
	FechaSalida  *string  `json:"fecha_salida"`
	Placa        string   `json:"placa"`
	Chofer       string   `json:"chofer"`
	Producto     string   `json:"producto"`
	PesoEntrada  float64  `json:"peso_entrada"`
	PesoSalida   *float64 `json:"peso_salida"`
	PesoNeto     *float64 `json:"peso_neto"`
	Unidad       string   `json:"unidad"`
	Estado       string   `json:"estado"`
}

var (
	db          *sql.DB
	weightRegex = regexp.MustCompile(`(ST|US),(GS|NT),([+-]?\d+\.?\d*)(kg|lb)`)
	clients     = make(map[*websocket.Conn]bool)
	clientsMu   sync.Mutex
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	serialMu     sync.Mutex
	serialCancel context.CancelFunc
	activePort   string
)

func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./pesajes.db")
	if err != nil {
		log.Fatalf("Error abriendo SQLite: %v", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS tickets (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		fecha TEXT NOT NULL,
		placa TEXT NOT NULL,
		chofer TEXT,
		producto TEXT,
		peso_bruto REAL NOT NULL,
		tara REAL NOT NULL,
		peso_neto REAL NOT NULL,
		unidad TEXT NOT NULL
	);`
	if _, err := db.Exec(query); err != nil {
		log.Fatalf("Error creando tabla: %v", err)
	}

	queryV2 := `
	CREATE TABLE IF NOT EXISTS tickets_v2 (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		fecha_entrada TEXT NOT NULL,
		fecha_salida TEXT,
		placa TEXT NOT NULL,
		chofer TEXT,
		producto TEXT,
		peso_entrada REAL NOT NULL,
		peso_salida REAL,
		peso_neto REAL,
		unidad TEXT NOT NULL,
		estado TEXT NOT NULL DEFAULT 'PENDIENTE'
	);`
	if _, err := db.Exec(queryV2); err != nil {
		log.Fatalf("Error creando tabla tickets_v2: %v", err)
	}
}

func parseXK3190(trama string) (*WeightReading, error) {
	trama = strings.TrimSpace(trama)
	matches := weightRegex.FindStringSubmatch(trama)
	if len(matches) < 5 {
		return nil, fmt.Errorf("formato desconocido: %s", trama)
	}

	val, err := strconv.ParseFloat(matches[3], 64)
	if err != nil {
		return nil, err
	}

	return &WeightReading{
		IsStable: matches[1] == "ST",
		Unit:     matches[4],
		Value:    val,
		Raw:      trama,
	}, nil
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func saveTicketHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req TicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	pesoNeto := req.PesoBruto - req.Tara
	fecha := time.Now().Format("2006-01-02 15:04:05")

	res, err := db.Exec(`
		INSERT INTO tickets (fecha, placa, chofer, producto, peso_bruto, tara, peso_neto, unidad)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fecha, req.Placa, req.Chofer, req.Producto, req.PesoBruto, req.Tara, pesoNeto, req.Unidad,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error al guardar: %v", err), http.StatusInternalServerError)
		return
	}

	id, _ := res.LastInsertId()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TicketResponse{
		ID:       id,
		Fecha:    fecha,
		PesoNeto: pesoNeto,
		Success:  true,
	})
}

func getTicketsHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	rows, err := db.Query(`
		SELECT id, fecha, placa, chofer, producto, peso_bruto, tara, peso_neto, unidad 
		FROM tickets 
		ORDER BY id DESC 
		LIMIT 15
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []TicketRecord
	for rows.Next() {
		var t TicketRecord
		if err := rows.Scan(&t.ID, &t.Fecha, &t.Placa, &t.Chofer, &t.Producto, &t.PesoBruto, &t.Tara, &t.PesoNeto, &t.Unidad); err == nil {
			list = append(list, t)
		}
	}

	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Error leyendo filas: %v", err), http.StatusInternalServerError)
		return
	}

	if list == nil {
		list = []TicketRecord{}
	}

	json.NewEncoder(w).Encode(list)
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			clientsMu.Lock()
			delete(clients, conn)
			clientsMu.Unlock()
			break
		}
	}
}

func broadcastWeight(reading *WeightReading) {
	data, err := json.Marshal(reading)
	if err != nil {
		return
	}

	clientsMu.Lock()
	defer clientsMu.Unlock()
	for conn := range clients {
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}
}

func startSerialReaderWithPort(ctx context.Context, port serial.Port) {
	defer port.Close()

	scanner := bufio.NewScanner(port)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			reading, err := parseXK3190(scanner.Text())
			if err == nil {
				broadcastWeight(reading)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error de lectura serial: %v", err)
	}
}

func restartSerial(portName string) error {
	serialMu.Lock()
	defer serialMu.Unlock()

	// Open and validate the port synchronously BEFORE canceling the old one
	mode := &serial.Mode{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := serial.Open(portName, mode)
	if err != nil {
		return fmt.Errorf("no se pudo abrir el puerto %s: %w", portName, err)
	}

	// Port opened successfully — now cancel the previous goroutine
	if serialCancel != nil {
		serialCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	serialCancel = cancel
	activePort = portName

	go startSerialReaderWithPort(ctx, port)
	return nil
}

func getPortsHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	ports, err := serial.GetPortsList()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error listando puertos: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ports":       ports,
		"active_port": activePort,
	})
}

func configPortHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	var req PortConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "JSON inválido",
		})
		return
	}
	if strings.TrimSpace(req.Port) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Puerto no especificado",
		})
		return
	}
	if err := restartSerial(req.Port); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"active_port": req.Port,
	})
}

func firstWeighHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	var req FirstWeighRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	fechaEntrada := time.Now().Format("2006-01-02 15:04:05")
	res, err := db.Exec(`
		INSERT INTO tickets_v2 (fecha_entrada, placa, chofer, producto, peso_entrada, unidad, estado)
		VALUES (?, ?, ?, ?, ?, ?, 'PENDIENTE')`,
		fechaEntrada, req.Placa, req.Chofer, req.Producto, req.PesoEntrada, req.Unidad,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error al guardar: %v", err), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"id":            id,
		"fecha_entrada": fechaEntrada,
	})
}

func getPendingHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query(`
		SELECT id, fecha_entrada, placa, chofer, producto, peso_entrada, unidad
		FROM tickets_v2
		WHERE estado = 'PENDIENTE'
		ORDER BY id DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type PendingTicket struct {
		ID           int64   `json:"id"`
		FechaEntrada string  `json:"fecha_entrada"`
		Placa        string  `json:"placa"`
		Chofer       string  `json:"chofer"`
		Producto     string  `json:"producto"`
		PesoEntrada  float64 `json:"peso_entrada"`
		Unidad       string  `json:"unidad"`
	}

	var list []PendingTicket
	for rows.Next() {
		var t PendingTicket
		if err := rows.Scan(&t.ID, &t.FechaEntrada, &t.Placa, &t.Chofer, &t.Producto, &t.PesoEntrada, &t.Unidad); err == nil {
			list = append(list, t)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Error leyendo filas: %v", err), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []PendingTicket{}
	}
	json.NewEncoder(w).Encode(list)
}

func secondWeighHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	var req SecondWeighRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	var pesoEntrada float64
	var placa, chofer, producto, fechaEntrada, unidad string
	err := db.QueryRow(`
		SELECT peso_entrada, placa, chofer, producto, fecha_entrada, unidad
		FROM tickets_v2 WHERE id = ? AND estado = 'PENDIENTE'`,
		req.TicketID,
	).Scan(&pesoEntrada, &placa, &chofer, &producto, &fechaEntrada, &unidad)
	if err != nil {
		http.Error(w, "Ticket no encontrado o ya completado", http.StatusNotFound)
		return
	}

	pesoNeto := math.Abs(pesoEntrada - req.PesoSalida)
	fechaSalida := time.Now().Format("2006-01-02 15:04:05")

	_, err = db.Exec(`
		UPDATE tickets_v2
		SET peso_salida = ?, peso_neto = ?, fecha_salida = ?, estado = 'COMPLETADO'
		WHERE id = ?`,
		req.PesoSalida, pesoNeto, fechaSalida, req.TicketID,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error actualizando ticket: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TicketV2Record{
		ID:           req.TicketID,
		FechaEntrada: fechaEntrada,
		FechaSalida:  &fechaSalida,
		Placa:        placa,
		Chofer:       chofer,
		Producto:     producto,
		PesoEntrada:  pesoEntrada,
		PesoSalida:   &req.PesoSalida,
		PesoNeto:     &pesoNeto,
		Unidad:       unidad,
		Estado:       "COMPLETADO",
	})
}

func getHistoryHandler(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	rows, err := db.Query(`
		SELECT id, fecha_entrada, fecha_salida, placa, chofer, producto,
		       peso_entrada, peso_salida, peso_neto, unidad, estado
		FROM tickets_v2
		ORDER BY id DESC
		LIMIT 15
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var list []TicketV2Record
	for rows.Next() {
		var t TicketV2Record
		if err := rows.Scan(&t.ID, &t.FechaEntrada, &t.FechaSalida, &t.Placa, &t.Chofer, &t.Producto,
			&t.PesoEntrada, &t.PesoSalida, &t.PesoNeto, &t.Unidad, &t.Estado); err == nil {
			list = append(list, t)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Error leyendo filas: %v", err), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []TicketV2Record{}
	}
	json.NewEncoder(w).Encode(list)
}

func main() {
	initDB()

	http.HandleFunc("/ws", wsHandler)

	http.HandleFunc("/api/ticket", saveTicketHandler)
	http.HandleFunc("/api/tickets", getTicketsHandler)

	http.HandleFunc("/api/ports", getPortsHandler)
	http.HandleFunc("/api/config/port", configPortHandler)
	http.HandleFunc("/api/ticket/first", firstWeighHandler)
	http.HandleFunc("/api/tickets/pending", getPendingHandler)
	http.HandleFunc("/api/ticket/second", secondWeighHandler)
	http.HandleFunc("/api/tickets/history", getHistoryHandler)

	fmt.Println("Backend Go operativo en http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
