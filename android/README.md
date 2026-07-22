# DVHub Remote for Android

Native Android controller for the 2E0LXY DVHub gateway. It uses the gateway's authenticated HTTPS API and WebSocket transport; it does not embed gateway or radio-network passwords.

## Download

**[Download DVHub Remote 1.0.2 APK](https://github.com/2E0LXY/dvhub-gateway/releases/download/android-v1.0.2/DVHub-Remote-1.0.2.apk)**

## Install

1. Download the release APK, or build `app/build/outputs/apk/debug/app-debug.apk`, and copy it to an Android 8.0 or newer device.
2. Permit installation from the browser or file manager used to open it.
3. In **Settings**, enter the HTTPS gateway URL, Basic Auth login, callsign, DMR ID and ESSID, then tap **Save encrypted & connect**.
4. Android asks for microphone permission the first time **HOLD TO TALK** is pressed.

Gateway credentials are encrypted with a non-exportable Android Keystore key. BrandMeister, TGIF, FreeSTAR and other network passwords are used only for the current screen/session and are cleared from the conference form after connection.

## Radio operation

- Select a network; its complete server-side talkgroup list is downloaded into the talkgroup selector.
- Connect the link before holding PTT. Releasing PTT stops microphone capture; a 90-second safety timer also stops it.
- Received 48 kHz mono PCM from the gateway plays through the device voice audio path.
- The TG 23530 bridge screen can start a 60-second test or a 15-minute session and has an explicit disconnect-all control.

Use radio-network access only in accordance with the licence conditions and network policies applicable to your callsign.
