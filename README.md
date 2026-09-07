# Sistema de Pesaje Industrial

Software de escritorio para captura, monitoreo en tiempo real, registro e impresión de boletas de pesaje industrial. Diseñado bajo una arquitectura **Sidecar**, desacoplando la comunicación de bajo nivel con el puerto serie (Go) de la interfaz gráfica del operador (Electron).

---

## Características

* **Monitoreo en Tiempo Real:** Transmisión continua de peso mediante WebSockets locales (<50 ms de latencia).
* **Filtro de Estabilidad:** Detección automática del estado del pesaje (`Estable` / `En Movimiento`). Bloqueo de registro preventivo si la báscula oscila.
* **Persistencia Local Integrada:** Motor embebido SQLite sin dependencias de CGO (`modernc.org/sqlite`).
* **Impresión Térmica:** Salida formateada a 80 mm para comprobantes no editables con cálculo automático:
  $$\text{Peso Neto} = \text{Peso Bruto} - \text{Tara}$$
* **Reimpresión y Auditoría:** Listado local de las últimas transacciones con correlativo autoincremental.

---

## Arquitectura

```text
[ Indicador XK3190-A12E ]
           │ (RS-232 / Serie 9600-8-N-1)
           ▼
[ scale-backend (Go) ] ──── HTTP/WebSocket ────▶ [ scale-frontend (Electron) ]
           │                                                    │
           ▼                                                    ▼
    [ pesajes.db ]                                      [ Impresora 80mm ]
```

* **Backend (Go):** Gestiona la conexión RS-232, parsea la trama de datos continua, emite eventos por WebSocket, expone la API REST y persiste las transacciones en SQLite.
* **Frontend (Electron):** Interfaz en modo oscuro orientada a casetas de pesaje, control de formularios de camión (placa, chofer, producto) y renderizado de comprobantes.
* **Simulador (Python + socat):** Emulador del indicador para pruebas locales sin hardware físico.

---

## Estructura del Proyecto

```text
Pesaje/
├── scale-backend/
│   ├── go.mod
│   ├── go.sum
│   ├── main.go             # Lector serial, WebSocket, API REST y SQLite
│   └── pesajes.db          # Base de datos generada automáticamente
├── scale-frontend/
│   ├── bin/                # Binario de Go para ejecución como sidecar
│   ├── index.html          # Dashboard principal de monitoreo
│   ├── ticket.html         # Plantilla térmica de impresión (80mm)
│   ├── main.js             # Proceso principal de Electron y orquestación
│   └── package.json
├── simulador_bascula.py     # Emulador de tramas continuas XK3190
└── .gitignore
```

---

## Requisitos

* **Go** 1.20 o superior
* **Node.js** 18 o superior y **npm**
* **Python 3** con `pyserial` (para pruebas con simulador)
* **socat** (en macOS/Linux para generación de puertos virtuales)

---

## Puesta en Marcha (Entorno de Desarrollo)

### 1. Generación de Puertos Virtuales (Simulación)

En una primera terminal, crea el puente serie virtual:

```bash
socat -d -d pty,raw,echo=0 pty,raw,echo=0
```
> El comando entregará dos puertos enlazados (ej: `/dev/ttys004` y `/dev/ttys005`).

### 2. Ejecutar el Simulador de Báscula

Configura el puerto emisor en `simulador_bascula.py` (ej. `/dev/ttys005`) y ejecuta:

```bash
python3 simulador_bascula.py
```

### 3. Compilar el Backend en Go

Asegúrate de que `main.go` esté configurado con el puerto receptor hermano (ej. `/dev/ttys004`):

```bash
cd scale-backend
go mod tidy
go build -o scale-backend main.go

# Copiar el binario a la carpeta del frontend para ejecución unificada
mkdir -p ../scale-frontend/bin
cp scale-backend ../scale-frontend/bin/
```

### 4. Iniciar la Aplicación

```bash
cd ../scale-frontend
npm install
npm start
```

---

## Protocolo de Comunicación Serial Soportado

Diseñado por defecto para indicadores compatibles con **Yaohua XK3190-A12E**:

| Parámetro | Valor |
| :--- | :--- |
| **Baud Rate** | 9600 |
| **Data Bits** | 8 |
| **Parity** | None |
| **Stop Bits** | 1 |
| **Formato de Trama** | `ST,GS,+01250.50kg\r\n` |

### Formato del Payload Regex

```regexp
(ST|US),(GS|NT),([+-]?\d+\.?\d*)(kg|lb)
```

* `ST`: Lectura estable | `US`: Lectura inestable (en oscilación).
* `GS`: Peso bruto | `NT`: Peso neto.
* Valor: Numérico flotante con signo.
* Unidad: `kg` o `lb`.
