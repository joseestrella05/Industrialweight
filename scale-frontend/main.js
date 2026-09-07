const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');
const { spawn } = require('child_process');

let mainWindow;
let backendProcess = null;
let currentTicketData = null;

function startBackend() {
  const isDev = !app.isPackaged;
  const isWindows = process.platform === 'win32';
  const binaryName = isWindows ? 'scale-backend.exe' : 'scale-backend';

  const backendPath = isDev
    ? path.join(__dirname, 'bin', binaryName)
    : path.join(process.resourcesPath, 'bin', binaryName);

  backendProcess = spawn(backendPath, [], {
    cwd: path.dirname(backendPath),
    stdio: 'ignore'
  });

  backendProcess.on('error', (err) => {
    console.error('Error iniciando el binario Go:', err);
  });
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1050,
    height: 800,
    minWidth: 850,
    minHeight: 650,
    backgroundColor: '#0f1115',
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false,
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'index.html'));
}

ipcMain.on('imprimir-ticket', (event, ticketData) => {
  currentTicketData = ticketData;

  let printWin = new BrowserWindow({
    width: 380,
    height: 600,
    title: 'Comprobante de Pesaje',
    show: true,
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false,
    },
  });

  printWin.loadFile(path.join(__dirname, 'ticket.html'));
});

ipcMain.on('obtener-datos-ticket', (event) => {
  event.returnValue = currentTicketData;
});

app.whenReady().then(() => {
  startBackend();
  createWindow();
});

app.on('before-quit', () => {
  if (backendProcess) {
    backendProcess.kill();
  }
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});