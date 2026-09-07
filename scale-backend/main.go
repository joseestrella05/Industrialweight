package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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

var (
	db          *sql.DB
	weightRegex = regexp.MustCompile(`(ST|US),(GS|NT),([+-]?\d+\.?\d*)(kg|lb)`)
	clients     = make(map[*websocket.Conn]bool)
	clientsMu   sync.Mutex
	upgrader    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
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

func saveTicketHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

func startSerialReader(portName string) {
	mode := &serial.Mode{
		BaudRate: 9600,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(portName, mode)
	if err != nil {
		log.Fatalf("Error en puerto serial %s: %v", portName, err)
	}
	defer port.Close()

	scanner := bufio.NewScanner(port)
	for scanner.Scan() {
		reading, err := parseXK3190(scanner.Text())
		if err == nil {
			broadcastWeight(reading)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error de lectura: %v", err)
	}
}

func main() {
	initDB()

	// Ajusta según el puerto hermano generado por socat
	go startSerialReader("/dev/ttys004")

	http.HandleFunc("/ws", wsHandler)
	http.HandleFunc("/api/ticket", saveTicketHandler)
	http.HandleFunc("/api/tickets", getTicketsHandler)

	fmt.Println("Backend Go operativo en http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
