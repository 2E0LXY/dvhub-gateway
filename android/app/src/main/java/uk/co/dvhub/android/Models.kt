package uk.co.dvhub.android

data class GatewaySettings(
    val serverUrl: String = "https://194.146.49.25",
    val username: String = "2E0LXY",
    val password: String = "",
    val callsign: String = "2E0LXY",
    val dmrId: String = "2344399",
    val essid: String = "01"
)

data class Network(val nodeId: Int, val label: String, val apiName: String, val target: String) {
    override fun toString() = label
}

data class Talkgroup(val id: Int, val name: String) {
    override fun toString() = "$id — $name"
}

data class RadioActivity(
    val identity: String,
    val nameLocation: String,
    val route: String,
    val metrics: String,
    val time: String
)

object GatewayNetworks {
    val all = listOf(
        Network(1, "FreeSTAR / System-X UK", "freestar", "FreeSTAR-SystemX-UK"),
        Network(2, "BrandMeister UK 2341", "brandmeister", "BrandMeister-UK-2341"),
        Network(3, "DMR+ FreeSTAR", "dmrplus", "DMRPlus-FreeSTAR"),
        Network(4, "TGIF", "tgif", "TGIF"),
        Network(5, "FreeDMR UK", "freedmr", "FreeDMR-UK"),
        Network(6, "Local YSF reflector", "ysf", "DVHub-YSF")
    )
}
