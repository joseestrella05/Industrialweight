const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');

let mainWindow;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 950,
    height: 700,
    backgroundColor: '#0f1115',
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false,
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'index.html'));
}

ipcMain.on('imprimir-ticket', (event, ticketData) => {
  let printWin = new BrowserWindow({
    width: 350,
    height: 500,
    show: true, // Ponlo en false cuando quieras impresión silenciosa directa
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false,
    },
  });

  printWin.loadFile(path.join(__dirname, 'ticket.html'));
  printWin.webContents.on('did-finish-load', () => {
    printWin.webContents.send('print-data', ticketData);
  });
});

app.whenReady().then(createWindow);
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });