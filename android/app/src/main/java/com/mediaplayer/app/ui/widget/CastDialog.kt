package com.mediaplayer.app.ui.widget

import android.app.Dialog
import android.content.Context
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.mediaplayer.app.R
import com.mediaplayer.app.util.NsdHelper
import kotlinx.coroutines.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

class CastDialog(
    context: Context,
    private val currentChannelId: Long,
    private val currentPosition: Long = 0L
) : Dialog(context, android.R.style.Theme_DeviceDefault_Dialog) {

    private lateinit var rvDevices: RecyclerView
    private lateinit var tvEmpty: TextView
    private lateinit var tvScanStatus: TextView
    private lateinit var progressScan: ProgressBar
    
    private val devices = mutableListOf<NsdHelper.DeviceInfo>()
    private lateinit var adapter: DeviceAdapter
    
    private val client = OkHttpClient()
    private val coroutineScope = CoroutineScope(Dispatchers.Main + SupervisorJob())

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.dialog_cast)
        
        window?.setLayout(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
        window?.setBackgroundDrawableResource(android.R.color.transparent)
        window?.clearFlags(android.view.WindowManager.LayoutParams.FLAG_DIM_BEHIND)
        window?.setDimAmount(0f)

        rvDevices = findViewById(R.id.rvDevices)
        tvEmpty = findViewById(R.id.tvEmpty)
        tvScanStatus = findViewById(R.id.tvScanStatus)
        progressScan = findViewById(R.id.progressScan)

        adapter = DeviceAdapter(devices) { device ->
            sendCastCommand(device)
        }
        rvDevices.layoutManager = LinearLayoutManager(context)
        rvDevices.adapter = adapter

        // 开始扫描局域网
        NsdHelper.startDiscovery(context, onDeviceFound = { device ->
            coroutineScope.launch {
                // 排除本机设备
                if (device.name == android.os.Build.MODEL) return@launch
                
                val existingIndex = devices.indexOfFirst { it.ip == device.ip }
                if (existingIndex == -1) {
                    // 新设备：加入列表
                    devices.add(device)
                    tvEmpty.visibility = View.GONE
                    if (devices.size == 1) rvDevices.requestFocus()
                } else {
                    // 已有同IP设备：端口可能已变（客户端重启），刷新为新端口
                    devices[existingIndex] = device
                }
                adapter.notifyDataSetChanged()
            }
        }, onDiscoveryStopped = {
            coroutineScope.launch {
                tvScanStatus.text = context.getString(R.string.cast_scan_finished)
                progressScan.visibility = View.GONE
                if (devices.isEmpty()) {
                    tvEmpty.visibility = View.VISIBLE
                }
            }
        })
        
        // 5秒后停止转圈
        coroutineScope.launch {
            delay(5000)
            if (isShowing) {
                tvScanStatus.text = context.getString(R.string.cast_online_devices)
                progressScan.visibility = View.GONE
                if (devices.isEmpty()) {
                    tvEmpty.visibility = View.VISIBLE
                }
            }
        }
    }
    
    private fun sendCastCommand(device: NsdHelper.DeviceInfo) {
        if (currentChannelId == -1L) {
            Toast.makeText(context, context.getString(R.string.toast_cast_no_channel), Toast.LENGTH_SHORT).show()
            return
        }
        
        val url = "http://${device.ip}:${device.port}/control/play"
        val json = """{"channel_id": $currentChannelId, "position": $currentPosition}"""
        val body = json.toRequestBody("application/json; charset=utf-8".toMediaTypeOrNull())
        
        val request = Request.Builder()
            .url(url)
            .post(body)
            .build()
            
        tvScanStatus.text = context.getString(R.string.cast_connecting_device, device.name)
        progressScan.visibility = View.VISIBLE
            
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                coroutineScope.launch {
                    tvScanStatus.text = context.getString(R.string.cast_online_devices)
                    progressScan.visibility = View.GONE
                    Toast.makeText(context, context.getString(R.string.toast_cast_connect_failed, e.message ?: ""), Toast.LENGTH_SHORT).show()
                }
            }

            override fun onResponse(call: Call, response: Response) {
                coroutineScope.launch {
                    tvScanStatus.text = context.getString(R.string.cast_online_devices)
                    progressScan.visibility = View.GONE
                    if (response.isSuccessful) {
                        Toast.makeText(context, context.getString(R.string.toast_cast_cmd_sent), Toast.LENGTH_SHORT).show()
                        dismiss()
                    } else {
                        Toast.makeText(context, context.getString(R.string.toast_cast_device_error), Toast.LENGTH_SHORT).show()
                    }
                }
            }
        })
    }

    override fun dismiss() {
        NsdHelper.stopDiscovery(context)
        coroutineScope.cancel()
        super.dismiss()
    }

    private inner class DeviceAdapter(
        private val list: List<NsdHelper.DeviceInfo>,
        private val onClick: (NsdHelper.DeviceInfo) -> Unit
    ) : RecyclerView.Adapter<DeviceAdapter.ViewHolder>() {

        inner class ViewHolder(view: View) : RecyclerView.ViewHolder(view) {
            val tvName: TextView = view.findViewById(R.id.tvDeviceName)
            val tvIp: TextView = view.findViewById(R.id.tvDeviceIp)
            
            init {
                view.setOnClickListener {
                    val pos = absoluteAdapterPosition
                    if (pos != RecyclerView.NO_POSITION) {
                        onClick(list[pos])
                    }
                }
            }
        }

        override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
            val view = LayoutInflater.from(parent.context).inflate(R.layout.item_cast_device, parent, false)
            return ViewHolder(view)
        }

        override fun onBindViewHolder(holder: ViewHolder, position: Int) {
            val device = list[position]
            holder.tvName.text = device.name
            holder.tvIp.text = "${device.ip}:${device.port}"
        }

        override fun getItemCount() = list.size
    }
}
