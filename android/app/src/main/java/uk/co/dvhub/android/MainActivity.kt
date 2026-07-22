package uk.co.dvhub.android

import android.Manifest
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.MotionEvent
import android.view.View
import android.widget.ArrayAdapter
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.app.AlertDialog
import androidx.core.content.ContextCompat
import androidx.recyclerview.widget.LinearLayoutManager
import com.google.gson.JsonElement
import com.google.gson.JsonObject
import uk.co.dvhub.android.databinding.*
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.net.URLEncoder

class MainActivity : AppCompatActivity(), GatewayClient.Listener {
    private lateinit var shell: ActivityMainBinding
    private lateinit var store: CredentialsStore
    private lateinit var client: GatewayClient
    private lateinit var audio: AudioEngine
    private var settings = GatewaySettings()
    private val handler = Handler(Looper.getMainLooper())
    private val activityAdapter = ActivityAdapter()
    private var currentNetwork = GatewayNetworks.all.first()
    private var currentTg = 23530
    private var linkActive = false
    private var pttPending = false
    private var dashboard: ViewDashboardBinding? = null
    private var link: ViewLinkBinding? = null
    private var conference: ViewConferenceBinding? = null
    private var settingsView: ViewSettingsBinding? = null

    private val micPermission = registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted && pttPending) beginPtt() else if (!granted) toast("Microphone permission is required to transmit")
        pttPending = false
    }

    private val poll = object : Runnable {
        override fun run() {
            refreshStatus()
            handler.postDelayed(this, 5_000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        shell = ActivityMainBinding.inflate(layoutInflater); setContentView(shell.root)
        store = CredentialsStore(this); settings = store.load()
        client = GatewayClient(this); client.configure(settings)
        audio = AudioEngine(client::sendAudio)
        shell.navigation.setOnItemSelectedListener {
            when (it.itemId) {
                R.id.nav_dashboard -> showDashboard()
                R.id.nav_link -> showLink()
                R.id.nav_conference -> showConference()
                R.id.nav_activity -> showActivity()
                R.id.nav_settings -> showSettings()
                else -> false
            }
        }
        showDashboard(); client.connect(); handler.post(poll)
    }

    override fun onDestroy() { handler.removeCallbacksAndMessages(null); client.close(); audio.release(); super.onDestroy() }

    private fun replace(view: View) { shell.content.removeAllViews(); shell.content.addView(view) }

    private fun showDashboard(): Boolean {
        val b = ViewDashboardBinding.inflate(layoutInflater); dashboard = b; replace(b.root)
        b.ysfStart.setOnClickListener { ysfControl("start") }; b.ysfRestart.setOnClickListener { ysfControl("restart") }
        b.ysfStop.setOnClickListener { ysfControl("stop") }; refreshStatus(); return true
    }

    private fun showLink(): Boolean {
        val b = ViewLinkBinding.inflate(layoutInflater); link = b; replace(b.root)
        b.networkSpinner.adapter = darkAdapter(GatewayNetworks.all)
        b.networkSpinner.setSelection(GatewayNetworks.all.indexOfFirst { it.nodeId == currentNetwork.nodeId }.coerceAtLeast(0))
        b.networkSpinner.onItemSelectedListener = SimpleItemSelected { position ->
            currentNetwork = GatewayNetworks.all[position]
            b.networkPassword.visibility = if (currentNetwork.requiresUserPassword) View.VISIBLE else View.GONE
            if (!currentNetwork.requiresUserPassword) b.networkPassword.text.clear()
            loadTalkgroups(currentNetwork, b)
        }
        b.linkButton.setOnClickListener {
            val tg = (b.talkgroupSpinner.selectedItem as? Talkgroup)?.id ?: currentTg
            val pass = b.networkPassword.text.toString()
            if (!linkActive && currentNetwork.requiresUserPassword && pass.isBlank()) { toast("Enter your user/hotspot credential"); return@setOnClickListener }
            linkActive = !linkActive; currentTg = tg
            if (!client.nodeState(currentNetwork, linkActive, tg, pass, b.dmrOptions.text.toString())) {
                linkActive = false; toast("WebSocket is not connected")
            }
            updateLinkUi(b)
        }
        b.pttButton.setOnTouchListener { _, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> { requestPtt(); true }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> { endPtt(); true }
                else -> true
            }
        }
        loadTalkgroups(currentNetwork, b); updateLinkUi(b); return true
    }

    private fun updateLinkUi(b: ViewLinkBinding) {
        b.linkButton.text = if (linkActive) "Disconnect link" else "Connect link"
        b.linkStatus.text = if (linkActive) "Connected: ${currentNetwork.label} · TG $currentTg" else "Disconnected"
        b.linkStatus.setTextColor(ContextCompat.getColor(this, if (linkActive) R.color.dv_green else R.color.dv_muted))
    }

    private fun requestPtt() {
        if (!linkActive) { toast("Connect a network before transmitting"); return }
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            pttPending = true; micPermission.launch(Manifest.permission.RECORD_AUDIO)
        } else beginPtt()
    }

    private fun beginPtt() {
        if (!client.txStart(currentNetwork.nodeId)) { toast("Gateway is offline"); return }
        if (!audio.startTx()) { client.txStop(); toast("Could not open the microphone"); return }
        link?.pttButton?.apply { text = "TRANSMITTING"; setBackgroundColor(Color.rgb(190, 35, 55)) }
        handler.postDelayed({ if (audioIsTransmitting()) { endPtt(); toast("PTT stopped at the 90-second safety limit") } }, 90_000)
    }

    private fun audioIsTransmitting() = link?.pttButton?.text == "TRANSMITTING"
    private fun endPtt() { audio.stopTx(); client.txStop(); link?.pttButton?.apply { text = "HOLD TO TALK"; setBackgroundColor(ContextCompat.getColor(this@MainActivity, R.color.dv_green_dark)) } }

    private fun showConference(): Boolean {
        val b = ViewConferenceBinding.inflate(layoutInflater); conference = b; replace(b.root)
        b.ysfDmrId.setText("2351633"); b.bridgeDmrId.setText(settings.dmrId); b.bridgeEssid.setText(settings.essid)
        b.testConference.setOnClickListener { startConference(b, 60, "test") }
        b.startConference.setOnClickListener { startPermanentConference(b) }
        b.stopConference.setOnClickListener { stopConference(b) }
        setupMatrix(b)
        refreshStatus(); return true
    }

    private fun startConference(b: ViewConferenceBinding, seconds: Int, action: String) {
        val bridgeId = b.bridgeDmrId.text.toString().toLongOrNull()
        val ysfId = b.ysfDmrId.text.toString().toLongOrNull()
        if (bridgeId == null || ysfId == null || b.bmPassword.text.isBlank() || b.tgifPassword.text.isBlank()) { toast("Enter both DMR IDs and the BrandMeister/TGIF user credentials"); return }
        val request = JsonObject().apply {
            addProperty("action", action); addProperty("callsign", settings.callsign); addProperty("dmr_id", ysfId)
            addProperty("duration_seconds", seconds)
        }
        client.post("/api/yorkshire_conference", request) { result -> runOnUiThread {
            result.onFailure { toast(it.message ?: "Conference failed") }.onSuccess {
                val original = settings
                val essid = b.bridgeEssid.text.toString().padStart(2, '0')
                settings = settings.copy(dmrId = bridgeId.toString(), essid = essid); client.configure(settings); client.connect()
                handler.postDelayed({
                    client.nodeState(GatewayNetworks.all[0], true, 23530, "")
                    client.nodeState(GatewayNetworks.all[1], true, 23530, b.bmPassword.text.toString())
                    client.nodeState(GatewayNetworks.all[3], true, 23530, b.tgifPassword.text.toString())
                    client.bridge(true, 1, 2, 4, 23530, 23530, 23530); settings = original
                    b.bmPassword.text.clear(); b.tgifPassword.text.clear()
                    b.legStatus.text = "Conference test active · TG 23530 · automatic stop in ${seconds}s"
                }, 800)
            }
        } }
    }

    private fun startPermanentConference(b: ViewConferenceBinding) {
        b.legStatus.text = "Starting permanent TG23530 conference…"
        client.post("/api/yorkshire_conference", JsonObject().apply { addProperty("action", "permanent") }) { result -> runOnUiThread {
            result.onFailure { toast(it.message ?: "Conference failed") }.onSuccess {
                b.bmPassword.text.clear(); b.tgifPassword.text.clear()
                b.legStatus.text = "Permanent TG23530 conference starting · automatic recovery enabled"
                handler.postDelayed({ refreshStatus() }, 2500)
            }
        } }
    }

    private fun stopConference(b: ViewConferenceBinding) {
        client.bridge(false, 1, 2, 4, 23530, 23530, 23530)
        listOf(0, 1, 3).forEach { client.nodeState(GatewayNetworks.all[it], false, 23530, "") }
        client.post("/api/yorkshire_conference", JsonObject().apply { addProperty("action", "stop") }) { result -> runOnUiThread {
            b.legStatus.text = if (result.isSuccess) "All conference legs disconnected" else "Stop returned: ${result.exceptionOrNull()?.message}"
        } }
    }

    private fun showActivity(): Boolean {
        val b = ViewActivityBinding.inflate(layoutInflater); replace(b.root)
        b.activityList.layoutManager = LinearLayoutManager(this); b.activityList.adapter = activityAdapter; return true
    }

    private fun showSettings(): Boolean {
        val b = ViewSettingsBinding.inflate(layoutInflater); settingsView = b; replace(b.root)
        b.serverUrl.setText(settings.serverUrl); b.username.setText(settings.username); b.password.setText(settings.password)
        b.callsign.setText(settings.callsign); b.dmrId.setText(settings.dmrId); b.essid.setText(settings.essid)
        b.dv30Count.adapter = darkAdapter(listOf("One DV30 / DV3000", "Two DV30 / DV3000"))
        b.lookupCallsign.setOnClickListener { lookupCallsign(b) }
        b.useHardwareVocoder.setOnClickListener {
            val first = b.dv30Address1.text.toString().trim(); val second = b.dv30Address2.text.toString().trim(); val count = b.dv30Count.selectedItemPosition + 1
            if (first.isBlank() || (count == 2 && second.isBlank())) { b.vocoderStatus.text = "Enter the required hardware address(es)"; return@setOnClickListener }
            val ok = client.setDv30(count, first, second) && client.setVocoder("hw")
            b.vocoderStatus.text = if (ok) "Hardware vocoder selected · $count device(s)" else "Gateway WebSocket is offline"
        }
        b.useSoftwareVocoder.setOnClickListener { b.vocoderStatus.text = if (client.setVocoder("sw")) "Software vocoder selected" else "Gateway WebSocket is offline" }
        b.saveSettings.setOnClickListener { saveSettings(b, true) }
        b.testSettings.setOnClickListener { saveSettings(b, false); client.get("/api/system") { result -> runOnUiThread {
            b.settingsStatus.text = if (result.isSuccess) "Connection successful — gateway authenticated" else "Connection failed: ${result.exceptionOrNull()?.message}"
        } } }
        return true
    }

    private fun saveSettings(b: ViewSettingsBinding, persist: Boolean) {
        val candidate = GatewaySettings(b.serverUrl.text.toString().trimEnd('/'), b.username.text.toString(), b.password.text.toString(),
            b.callsign.text.toString().uppercase(Locale.UK), b.dmrId.text.toString(), b.essid.text.toString().padStart(2, '0'))
        if (!candidate.serverUrl.startsWith("https://") || candidate.password.isBlank() || candidate.dmrId.length != 7) {
            b.settingsStatus.text = "Use an HTTPS URL, gateway password and valid 7-digit DMR ID"; return
        }
        settings = candidate; if (persist) store.save(settings); client.configure(settings); client.connect()
        b.settingsStatus.text = if (persist) "Saved encrypted and connecting…" else "Testing…"
    }

    private fun lookupCallsign(b: ViewSettingsBinding) {
        val call = b.callsign.text.toString().trim().uppercase(Locale.UK)
        if (call.isBlank()) { b.lookupResult.text = "Enter a callsign first"; return }
        client.get("/api/dmr_lookup?callsign=${URLEncoder.encode(call, Charsets.UTF_8.name())}") { result -> runOnUiThread {
            result.onFailure { b.lookupResult.text = "Lookup failed: ${it.message}" }.onSuccess { json ->
                val rows = json.getAsJsonArray("results")?.mapNotNull { it.takeIf(JsonElement::isJsonObject)?.asJsonObject }.orEmpty()
                if (rows.isEmpty()) { b.lookupResult.text = "No DMR IDs found for $call"; return@onSuccess }
                val labels = rows.map { row ->
                    val id = first(row, "radio_id", "dmr_id", "id") ?: "—"; val name = first(row, "name") ?: "Unknown name"
                    val place = listOfNotNull(first(row,"city"), first(row,"state"), first(row,"country")).joinToString(", ")
                    "$id · $name · $place"
                }
                b.lookupResult.text = "${rows.size} match(es):\n" + labels.joinToString("\n")
                if (rows.size == 1) b.dmrId.setText(first(rows[0], "radio_id", "dmr_id", "id"))
                else AlertDialog.Builder(this).setTitle("Select the correct DMR ID").setItems(labels.toTypedArray()) { _, which ->
                    b.dmrId.setText(first(rows[which], "radio_id", "dmr_id", "id")); b.lookupResult.text = "Selected ${labels[which]}"
                }.show()
            }
        } }
    }

    private fun setupMatrix(b: ViewConferenceBinding) {
        val networks = GatewayNetworks.all.filter { it.nodeId in 1..5 }
        val third = listOf(Network(0, "No third leg", "none", "")) + networks
        b.matrixNetworkA.adapter = darkAdapter(networks); b.matrixNetworkB.adapter = darkAdapter(networks); b.matrixNetworkB.setSelection(1)
        b.matrixNetworkC.adapter = darkAdapter(third)
        b.matrixNetworkA.onItemSelectedListener = SimpleItemSelected { loadMatrixTalkgroups(networks[it], b.matrixTgA) }
        b.matrixNetworkB.onItemSelectedListener = SimpleItemSelected { loadMatrixTalkgroups(networks[it], b.matrixTgB) }
        b.matrixNetworkC.onItemSelectedListener = SimpleItemSelected { if (it == 0) b.matrixTgC.adapter = darkAdapter(listOf(Talkgroup(23530,"Unused"))) else loadMatrixTalkgroups(third[it], b.matrixTgC) }
        b.connectMatrix.setOnClickListener {
            val a = b.matrixNetworkA.selectedItem as Network; val bb = b.matrixNetworkB.selectedItem as Network; val c = b.matrixNetworkC.selectedItem as Network
            val aTg = (b.matrixTgA.selectedItem as Talkgroup).id; val bTg = (b.matrixTgB.selectedItem as Talkgroup).id; val cTg = (b.matrixTgC.selectedItem as Talkgroup).id
            if (a.nodeId == bb.nodeId || (c.nodeId != 0 && c.nodeId in listOf(a.nodeId, bb.nodeId))) { b.matrixStatus.text = "Select two or three different networks"; return@setOnClickListener }
            val ok = client.bridge(true, a.nodeId, bb.nodeId, c.nodeId.takeIf { it != 0 }, aTg, bTg, cTg)
            b.matrixStatus.text = if (ok) "Connected: ${a.label} TG $aTg ↔ ${bb.label} TG $bTg" + if (c.nodeId != 0) " ↔ ${c.label} TG $cTg" else "" else "Gateway WebSocket is offline"
        }
        b.disconnectMatrix.setOnClickListener { client.bridge(false, 1, 2); b.matrixStatus.text = "Matrix disconnected" }
    }

    private fun loadMatrixTalkgroups(network: Network, spinner: android.widget.Spinner) {
        val networkKey = URLEncoder.encode(network.target, Charsets.UTF_8.name())
        client.get("/api/talkgroups?network=$networkKey") { result -> runOnUiThread {
            val fallback = listOf(Talkgroup(23530, "Yorkshire / DVHub"), Talkgroup(9, "Local"), Talkgroup(235, "United Kingdom"))
            val parsed = result.getOrNull()?.let(::parseTalkgroups).orEmpty().ifEmpty { fallback }; spinner.adapter = darkAdapter(parsed)
            parsed.indexOfFirst { it.id == 23530 }.takeIf { it >= 0 }?.let(spinner::setSelection)
        } }
    }

    private fun refreshStatus() {
        client.get("/api/system") { result -> result.onSuccess { json -> runOnUiThread {
            dashboard?.systemSummary?.text = systemText(json)
            dashboard?.serviceSummary?.text = servicesText(json)
        } } }
        client.get("/api/ysf_dashboard") { result -> result.onSuccess { json -> runOnUiThread {
            dashboard?.ysfSummary?.text = ysfText(json)
        } } }
        client.get("/api/yorkshire_conference") { result -> result.onSuccess { json -> runOnUiThread {
            val status = conferenceText(json); dashboard?.conferenceSummary?.text = status; conference?.legStatus?.text = status
        } } }
    }

    private fun ysfControl(action: String) {
        client.post("/api/ysf_control", JsonObject().apply { addProperty("action", action) }) { result -> runOnUiThread {
            toast(if (result.isSuccess) "YSF reflector: $action command accepted" else result.exceptionOrNull()?.message ?: "YSF command failed")
            handler.postDelayed({ refreshStatus() }, 800)
        } }
    }

    private fun loadTalkgroups(network: Network, b: ViewLinkBinding) {
        val fallback = listOf(Talkgroup(23530, "Yorkshire / DVHub"), Talkgroup(9, "Local"), Talkgroup(91, "Worldwide"), Talkgroup(235, "United Kingdom"))
        val networkKey = URLEncoder.encode(network.target, Charsets.UTF_8.name())
        client.get("/api/talkgroups?network=$networkKey") { result -> runOnUiThread {
            val parsed = result.getOrNull()?.let(::parseTalkgroups).orEmpty().ifEmpty { fallback }
            b.talkgroupSpinner.adapter = darkAdapter(parsed)
            val selected = parsed.indexOfFirst { it.id == currentTg }.let { if (it < 0) 0 else it }; b.talkgroupSpinner.setSelection(selected)
        } }
    }

    private fun parseTalkgroups(json: JsonObject): List<Talkgroup> {
        val array = listOf("talkgroups", "items", "data").firstNotNullOfOrNull { json.get(it)?.takeIf(JsonElement::isJsonArray)?.asJsonArray } ?: return emptyList()
        return array.mapNotNull { item ->
            if (item.isJsonPrimitive) item.asInt.let { Talkgroup(it, "Talkgroup $it") }
            else item.asJsonObject.let { o ->
                val id = first(o, "id", "tg", "talkgroup")?.toIntOrNull() ?: return@mapNotNull null
                Talkgroup(id, first(o, "name", "label", "description") ?: "Talkgroup $id")
            }
        }.distinctBy { it.id }.sortedBy { it.id }
    }

    private fun systemText(j: JsonObject): String = "Host ${first(j,"hostname","host") ?: "gateway"}  ·  CPU ${first(j,"cpu_load","cpu","load") ?: "—"}%\nMemory ${first(j,"memory","memory_used","ram") ?: "—"}  ·  Uptime ${first(j,"uptime") ?: "—"}"
    private fun servicesText(j: JsonObject): String = "Gateway ${deep(j,"dvhub-gateway") ?: deep(j,"gateway") ?: "online"}  ·  YSF ${deep(j,"ysfreflector") ?: "—"}  ·  Caddy ${deep(j,"caddy") ?: "—"}"
    private fun ysfText(j: JsonObject): String = "${first(j,"name","reflector_name") ?: "Yorkshire Link HUB"}  ·  ID ${first(j,"id","reflector_id","number") ?: "not registered"}\nStatus ${first(j,"status","state") ?: "—"}  ·  Clients ${first(j,"clients","client_count","connected_count") ?: "0"}  ·  TX today ${first(j,"transmissions_today") ?: "0"}\n${first(j,"host") ?: settings.serverUrl} : ${first(j,"port") ?: "42000"}  ·  Uptime ${first(j,"uptime_seconds") ?: "—"}s"
    private fun conferenceText(j: JsonObject): String {
        val mode = if (j.get("permanent")?.asBoolean == true) "permanent" else "temporary"
        return "Conference ${first(j,"active","status","state") ?: "inactive"} · $mode · TG ${first(j,"tg","talkgroup") ?: "23530"}\nYSF ${deep(j,"ysf") ?: "—"} · FreeSTAR ${deep(j,"freestar") ?: "—"} · BM ${deep(j,"brandmeister") ?: "—"} · TGIF ${deep(j,"tgif") ?: "—"}"
    }

    override fun onConnection(connected: Boolean, message: String) = runOnUiThread {
        shell.connectionBadge.text = if (connected) "ONLINE" else "OFFLINE"
        shell.connectionBadge.setTextColor(ContextCompat.getColor(this, if (connected) R.color.dv_green else R.color.dv_warning))
        link?.linkStatus?.let { if (!connected) it.text = message }; settingsView?.settingsStatus?.text = message
    }

    override fun onJson(json: JsonObject) = runOnUiThread {
        val kind = first(json, "type", "event", "cmd") ?: ""
        if (kind == "tx_status") {
            when (first(json, "state")) {
                "busy", "denied" -> {
                    if (audioIsTransmitting()) endPtt()
                    val reason = first(json, "reason") ?: "Transmit permission denied"
                    link?.linkStatus?.text = reason
                    toast(reason)
                }
                "active" -> if (!audioIsTransmitting()) {
                    link?.linkStatus?.text = "RX only · transmitter in use by ${first(json, "callsign", "dmr_id") ?: "another operator"}"
                }
                "idle" -> if (!audioIsTransmitting()) updateLinkUi(link ?: return@runOnUiThread)
            }
        }
        if (kind.contains("traffic", true) || json.has("callsign") || json.has("source_id")) {
            val call = first(json,"callsign","source_callsign","call") ?: "Unknown"
            val id = first(json,"dmr_id","source_id","id") ?: "—"
            val name = first(json,"name","operator_name") ?: "Unknown operator"
            val location = listOfNotNull(first(json,"city","location"), first(json,"country")).distinct().joinToString(", ").ifBlank { "Location unavailable" }
            val source = first(json,"network","source","from") ?: "Gateway"; val tg = first(json,"tg","talkgroup","destination") ?: "—"
            val ber = first(json,"ber")?.let { "BER $it" } ?: "BER —"; val loss = first(json,"loss","packet_loss")?.let { "Loss $it" } ?: "Loss —"
            activityAdapter.add(RadioActivity("$call · ID $id", "$name · $location", "$source → TG $tg", "$ber · $loss", SimpleDateFormat("HH:mm:ss", Locale.UK).format(Date())))
        }
    }

    override fun onAudio(bytes: ByteArray) = audio.play(bytes)
    private fun first(o: JsonObject, vararg keys: String): String? = keys.firstNotNullOfOrNull { k -> o.get(k)?.takeUnless { it.isJsonNull || it.isJsonObject || it.isJsonArray }?.asString }
    private fun deep(o: JsonObject, key: String): String? {
        o.entrySet().forEach { (k, v) ->
            if (k.equals(key, true)) return if (v.isJsonObject) first(v.asJsonObject,"status","state","active") ?: v.toString() else v.asString
            if (v.isJsonObject) deep(v.asJsonObject, key)?.let { return it }
        }; return null
    }
    private fun <T> darkAdapter(items: List<T>) = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, items)
    private fun toast(text: String) = Toast.makeText(this, text, Toast.LENGTH_LONG).show()
}

private class SimpleItemSelected(private val select: (Int) -> Unit) : android.widget.AdapterView.OnItemSelectedListener {
    override fun onItemSelected(parent: android.widget.AdapterView<*>?, view: View?, position: Int, id: Long) = select(position)
    override fun onNothingSelected(parent: android.widget.AdapterView<*>?) = Unit
}
