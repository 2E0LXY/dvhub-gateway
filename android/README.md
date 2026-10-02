# DVHub Remote for Android

Native Android controller for the 2E0LXY DVHub gateway. It uses the gateway's authenticated HTTPS API and WebSocket transport; it does not embed gateway or radio-network passwords.

## Download

**[Download the latest Yorkshire Link HUB APK](https://github.com/2E0LXY/dvhub-gateway/releases)**

## Install

1. Download the release APK, or build `app/build/outputs/apk/debug/app-debug.apk`, and copy it to an Android 8.0 or newer device.
2. Permit installation from the browser or file manager used to open it.
3. In **Settings**, enter the HTTPS gateway URL, Basic Auth login, callsign, DMR ID and ESSID, then tap **Save encrypted & connect**.
4. Android asks for microphone permission the first time **HOLD TO TALK** is pressed.

Gateway credentials are encrypted with a non-exportable Android Keystore key. Passwords entered for individual or 60-second test connections are used only for the current screen/session and cleared afterward. Permanent conference mode uses the gateway's protected server-side configuration and never downloads those network passwords to Android.

## Radio operation

- Select a network; its complete server-side talkgroup list is downloaded into the talkgroup selector.
- Connect the link before holding PTT. Releasing PTT stops microphone capture; a 90-second safety timer also stops it.
- Gateway 8 kHz/20 ms PCM is resampled to and from the Android 48 kHz voice audio path.
- The TG 23530 bridge screen can start a 60-second test or permanent server-managed conference and has an explicit disconnect-all control.
- The status screen configures and controls native AllStar node 530471 and reports linked nodes without exposing its node password.
- P25, NXDN and DV30 health are visible in the multimode summary.
- Hybrid mode shares one or two configured DV30/DV3000 devices across browser, P25 and AllStar traffic through the local broker, with software overflow and hardware-failure fallback.

Use radio-network access only in accordance with the licence conditions and network policies applicable to your callsign.
