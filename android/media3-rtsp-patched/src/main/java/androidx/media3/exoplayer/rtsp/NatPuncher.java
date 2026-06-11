package androidx.media3.exoplayer.rtsp;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

public class NatPuncher {
    public static final Map<Integer, DatagramSocket> sockets = new ConcurrentHashMap<>();

    public static void punch(String serverIp, int serverPort, int localPort) {
        DatagramSocket socket = sockets.get(localPort);
        if (socket != null) {
            try {
                // Send a 1-byte dummy packet to punch the NAT hole
                byte[] dummy = new byte[1];
                dummy[0] = 0x00;
                DatagramPacket packet = new DatagramPacket(dummy, 1, InetAddress.getByName(serverIp), serverPort);
                socket.send(packet);
                RtspMessageLogger.d("NatPuncher", "Punched NAT hole to " + serverIp + ":" + serverPort + " from local port " + localPort);
            } catch (Exception e) {
                RtspMessageLogger.e("NatPuncher", "Failed to punch NAT hole", e);
            }
        } else {
            RtspMessageLogger.w("NatPuncher", "Cannot punch hole: no socket registered for local port " + localPort);
        }
    }
}
