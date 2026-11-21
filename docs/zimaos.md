# SHELFCODE on ZimaOS (and CasaOS)

SHELFCODE runs on ZimaOS as a normal app with its own tile on the dashboard. Its data is kept in `/DATA/AppData/shelfcode` on your ZimaOS machine.

## Install (about one minute)

1. Download [`shelfcode-zimaos.yml`](https://github.com/ABelly99/Shelf-Code/releases/latest/download/shelfcode-zimaos.yml) to the computer you use to open ZimaOS.
2. Open your ZimaOS dashboard in the browser and go to the **App Store**.
3. Click the **+** button at the top right (**Custom Install**).
4. Click **Import** at the top right of the window that opens.
5. Upload `shelfcode-zimaos.yml`, or paste its contents into the Docker Compose box. Click **Submit**.
6. Check the settings (port **8765**, data folder `/DATA/AppData/shelfcode`) and click **Install**.

When it's done, a **SHELFCODE** tile appears on the dashboard. Click it, or open `http://<your-zimaos-ip>:8765` from any device on your network.

> If port 8765 is already used on your ZimaOS, change the **Host** port in step 6 (for example to 8766). Then open that port instead.

### Option B: add SHELFCODE as an app source

This makes SHELFCODE show up in the ZimaOS App Store itself:

1. App Store → **app source settings** (the gear or "More apps" button next to the search box).
2. Add this source URL: `https://github.com/ABelly99/Shelf-Code/releases/latest/download/shelfcode-zimaos-appstore.zip`
3. Search the App Store for **SHELFCODE** and click **Install**.

Option A (Custom Install) works on every ZimaOS and CasaOS version. Use it if your version has no place to add app sources.

## First steps

- Pick what you sell (Clothing, Phones…) on the welcome screen.
- **Settings → Network → PIN**: set a PIN if other people use your network. Every device then asks for it once.
- **Settings → Scanner**: enter the connecting code from the Barcode Scanner phone app.

## Update

**Restarting the app installs the newest version** automatically (tile → ⋮ → Restart, or restart ZimaOS). You can also update the image:

The app tile → **⋮ → Check then update**. You can also go to App Store → Installed → SHELFCODE → **Update** (the app uses the `latest` image). Your data in `/DATA/AppData/shelfcode` is kept.

## Backups

SHELFCODE saves a `.zip` backup every day to `/DATA/AppData/shelfcode/backups`. You can see these files in the ZimaOS **Files** app, and copy them to another drive or to the cloud with ZimaOS's backup tools.

## Uninstall

Right-click the tile → **Uninstall**. To delete the data too, remove `/DATA/AppData/shelfcode` in the Files app.

## Troubleshooting

- **The app doesn't start / "image not found"**: open the app's settings and check the image is `ghcr.io/abelly99/shelf-code:latest`. Your ZimaOS also needs internet access to download it.
- **Can't open it from a phone**: use the ZimaOS machine's IP address (shown in ZimaOS under Settings → Network), not `localhost`.
- **Logs**: the app tile → **⋮ → Logs**.
