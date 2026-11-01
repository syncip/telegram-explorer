# Telegram Explorer

Dateispeicher im Windows-Explorer-Stil, der deinen Telegram-Account als Ablage nutzt. Dateien werden optional verschlüsselt, bevor sie den Server verlassen, und sind auch per SFTP erreichbar.

**Telegram ist kein Backup.** Wird der Account gesperrt oder der Speicherkanal gelöscht, sind die Dateien weg. Für Wichtiges brauchst du eine zweite Kopie.

## Start

1. Bei [my.telegram.org](https://my.telegram.org) unter „API development tools“ App-ID und Hash holen.
2. `.env` anlegen und ausfüllen:

   ```bash
   cp .env.example .env
   ```

3. Starten und den Einrichtungscode aus dem Log lesen:

   ```bash
   docker compose up -d --build
   ```

   ```bash
   docker compose logs telex
   ```

4. `http://127.0.0.1:8080` öffnen (auf einem Server über den Reverse-Proxy), Konto und Passphrase anlegen, dann mit der Telefonnummer bei Telegram anmelden.

Nach jedem Neustart wird die Passphrase einmal im Browser abgefragt. Bis dahin sind Uploads, Freigaben und SFTP inaktiv. Ohne Passphrase sind verschlüsselte Dateien verloren, niemand kann sie zurücksetzen.

### Reverse-Proxy

Der Web-Port ist nur an `127.0.0.1` gebunden. Beispiel für Caddy:

```
speicher.example.de {
    reverse_proxy 127.0.0.1:8080
}
```

Mit nginx brauchst du zusätzlich `client_max_body_size 64m;`, `proxy_request_buffering off;`, `proxy_buffering off;` und `proxy_read_timeout 1h;`. Für den Betrieb ohne Proxy `TRUST_PROXY=0` setzen und die Port-Zeile auf `8080:8080` ändern. Passwörter laufen dann unverschlüsselt durchs Netz, das ist nur im eigenen Heimnetz vertretbar.

## SFTP

SFTP bleibt aus, bis du unter **Einstellungen** ein eigenes SFTP-Kennwort setzt. Es läuft auf Port 2222 mit deinem Benutzernamen. Mit **SFTP abschalten** werden der Port geschlossen und laufende Sitzungen beendet. FTP ist bewusst nicht eingebaut, weil es Kennwörter im Klartext überträgt.

```bash
sftp -P 2222 benutzer@speicher.example.de
```

Beim Schreiben landet die Datei erst im Zwischenspeicher und wird beim Schließen als neue Version zu Telegram übertragen. Die alte Version liegt danach im Papierkorb.

## Verschlüsselung

| | Verschlüsselt | Unverschlüsselt |
|---|---|---|
| Telegram sieht | Zufällige Daten, zufälliger Name, Größe, Zeitpunkt | Inhalt und Dateiname |
| In Telegram herunterladbar | Nein | Ja |
| Dateien über 2.000 MiB | Blöcke von 1 GiB | Blöcke `name.part001`, `name.part002` usw. |

Der Schalter in der Symbolleiste gilt für den nächsten Upload. Die Voreinstellung pro Ordner steht unter **Eigenschaften** und gilt auch für SFTP. Standard ist „verschlüsselt“.

Verfahren: Argon2id leitet aus der Passphrase einen Schlüssel ab, der den Master-Key schützt. Jede Datei hat einen eigenen Schlüssel und wird in Segmenten mit ChaCha20-Poly1305 verschlüsselt. Geänderte, vertauschte oder gekürzte Daten fallen beim Lesen auf.

Grenzen: Der Server verschlüsselt, nicht der Browser. Auf deinem Server existieren Dateien daher zeitweise im Klartext, nämlich im Zwischenspeicher `/data/stage`, bis der Upload abgeschlossen ist. Dateinamen liegen unverschlüsselt in der Datenbank `/data/telex.db`. Schütze den Server entsprechend, am besten mit einer verschlüsselten Platte.

## Betrieb

- **Speicherplatz:** Der Server braucht freien Platz für alle gleichzeitig laufenden Uploads, mindestens die größte Datei.
- **Backup:** Alle 6 Stunden landet ein verschlüsselter Stand der Datenbank als `telex-backup-*.telexbk` im Speicherkanal, die letzten 5 bleiben erhalten.
- **Papierkorb:** Einträge werden nach 30 Tagen endgültig gelöscht, dann auch bei Telegram.
- **Speicherkanal:** Heißt „Telegram Explorer Storage“. Dort keine Nachrichten von Hand löschen.

### Wiederherstellen

Wenn `/data` verloren ist: Das neueste Backup in Telegram herunterladen, in ein leeres Volume legen und einspielen. Die Passphrase wird abgefragt.

```bash
docker compose run --rm -v "$PWD/backup.telexbk:/backup.telexbk:ro" telex restore /backup.telexbk
```

## Entwicklung

Ohne Telegram und Docker, mit lokalem Testspeicher (PowerShell-Variante analog):

```bash
cd web/ui && npm ci && npm run build && cd ../..
```

```bash
DATA_DIR=./data STORAGE_BACKEND=local go run ./cmd/telex
```

```bash
go test ./...
```
