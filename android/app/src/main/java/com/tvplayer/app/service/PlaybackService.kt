package com.tvplayer.app.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer

/**
 * Background playback service for Android TV.
 * Keeps audio playing when app goes to background (e.g., picture-in-picture).
 */
class PlaybackService : Service() {

    private var player: ExoPlayer? = null
    private val CHANNEL_ID = "tvplayer_playback"

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val url = intent?.getStringExtra("stream_url") ?: return START_NOT_STICKY
        val name = intent.getStringExtra("channel_name") ?: "播放中"

        // Start foreground
        val notification = buildNotification(name)
        startForeground(1, notification)

        // Initialize player
        if (player == null) {
            player = ExoPlayer.Builder(this).build()
        }

        player?.apply {
            setMediaItem(MediaItem.fromUri(url))
            prepare()
            playWhenReady = true

            addListener(object : Player.Listener {
                override fun onPlaybackStateChanged(playbackState: Int) {
                    if (playbackState == Player.STATE_ENDED) {
                        stopSelf()
                    }
                }
            })
        }

        return START_STICKY
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "播放服务",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "后台播放通知"
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(channelName: String): Notification {
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("电视播放器")
            .setContentText("正在播放: $channelName")
            .setSmallIcon(android.R.drawable.ic_media_play)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        player?.release()
        player = null
        super.onDestroy()
    }
}
