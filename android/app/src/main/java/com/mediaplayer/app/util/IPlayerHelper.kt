package com.mediaplayer.app.util

interface IPlayerHelper {
    /**
     * Start playback
     */
    fun play(url: String, userAgent: String = "", customHeaders: String = "")
    
    fun pause()
    fun resume()
    fun stop()
    fun release()
    
    fun isPlaying(): Boolean
    fun getTime(): Long
    fun setTime(timeMs: Long)
    fun setRate(rate: Float)
    
    /**
     * Settings passthrough
     * @param scaleMode 0=Auto, 1=Stretch, 2=Crop, 3=4:3
     */
    fun setAspectRatio(scaleMode: Int)
    
    /**
     * Settings passthrough
     * @param mode 0=Auto, 1=Hardware, 2=Software
     */
    fun setDecoderMode(mode: Int)
    
    /**
     * Settings passthrough
     * @param cacheMs 0=Auto, or value in milliseconds
     */
    fun setCacheDuration(cacheMs: Int)

    interface PlayerListener {
        /**
         * @param percent 0-100 float representing buffer percentage
         */
        fun onBuffering(percent: Float)
        
        /**
         * Called when playback officially starts
         * @param resolution Video resolution string e.g. "1920x1080"
         */
        fun onPlaying(resolution: String)
        
        /**
         * Called on playback error
         */
        fun onError()
    }
}
