import serial
import time

# Reemplaza con uno de los puertos que te entregó socat
PUERTO = '/dev/ttys008'
BAUDIOS = 9600

try:
    ser = serial.Serial(PUERTO, BAUDIOS, timeout=1)
    print(f"Simulador de báscula emitiendo en {PUERTO}...")
    
    peso_simulado = 1500.50  # 1,250.50 kg

    while True:
        # Formato continuo típico de la familia XK3190:
        # Signo, peso formateado a 6 dígitos con decimales, estado ST (estable)
        trama = f"ST,GS,+{peso_simulado:08.2f}kg\r\n"
        ser.write(trama.encode('ascii'))
        time.sleep(0.1)  # Emite 10 tramas por segundo

except serial.SerialException as e:
    print(f"Error al abrir el puerto: {e}")