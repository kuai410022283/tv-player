package com.mediaplayer.licensegen

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Bundle
import android.widget.ArrayAdapter
import android.widget.Button
import com.google.android.material.textfield.MaterialAutoCompleteTextView
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.datepicker.MaterialDatePicker
import com.google.android.material.textfield.TextInputLayout
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

class MainActivity : AppCompatActivity() {

    private lateinit var machineCodeInput: EditText
    private lateinit var machineCodeLayout: TextInputLayout
    private lateinit var expireDropdown: MaterialAutoCompleteTextView
    private lateinit var expireLayout: TextInputLayout
    private lateinit var generateBtn: Button
    private lateinit var resultContainer: android.view.View
    private lateinit var resultText: TextView
    private lateinit var copyBtn: Button
    private lateinit var detailText: TextView

    private var customDate: String? = null
    private val dateFormat = SimpleDateFormat("yyyy-MM-dd", Locale.US)

    private val expireOptions = listOf("30天", "90天", "365天", "永久", "自定义日期")

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        machineCodeInput = findViewById(R.id.machine_code_input)
        machineCodeLayout = findViewById(R.id.machine_code_layout)
        expireDropdown = findViewById(R.id.expire_dropdown)
        expireLayout = findViewById(R.id.expire_layout)
        generateBtn = findViewById(R.id.generate_btn)
        resultContainer = findViewById(R.id.result_container)
        resultText = findViewById(R.id.result_text)
        copyBtn = findViewById(R.id.copy_btn)
        detailText = findViewById(R.id.detail_text)

        setupExpireDropdown()
        setupButtons()
    }

    private fun setupExpireDropdown() {
        val adapter = ArrayAdapter(this, android.R.layout.simple_dropdown_item_1line, expireOptions)
        expireDropdown.setAdapter(adapter)
        expireDropdown.setText("30天", false)

        expireDropdown.setOnItemClickListener { _, _, position, _ ->
            customDate = null
            if (position == 4) { // 自定义日期
                showDatePicker()
            }
        }
    }

    private fun showDatePicker() {
        val datePicker = MaterialDatePicker.Builder.datePicker()
            .setTitleText("选择过期日期")
            .setSelection(MaterialDatePicker.todayInUtcMilliseconds())
            .build()

        datePicker.addOnPositiveButtonClickListener { millis ->
            customDate = dateFormat.format(Date(millis))
            expireDropdown.setText(customDate, false)
        }

        datePicker.show(supportFragmentManager, "date_picker")
    }

    private fun setupButtons() {
        generateBtn.setOnClickListener {
            generateLicense()
        }

        copyBtn.setOnClickListener {
            copyToClipboard()
        }
    }

    private fun generateLicense() {
        val machineCode = machineCodeInput.text.toString().trim().lowercase()

        // 验证机器码
        if (machineCode.isEmpty()) {
            machineCodeLayout.error = "请输入机器码"
            return
        }
        if (machineCode.length != 16 || !machineCode.all { it in '0'..'9' || it in 'a'..'f' }) {
            machineCodeLayout.error = "机器码应为16位十六进制字符"
            return
        }
        machineCodeLayout.error = null

        // 解析过期时间 → 调用 Go AAR 生成
        val selected = expireDropdown.text.toString()
        expireLayout.error = null

        try {
            val licenseKey: String
            val expireDateForDisplay: String

            when {
                selected == "永久" -> {
                    licenseKey = licensegen.Licensegen.generate(machineCode, 0)
                    expireDateForDisplay = "permanent"
                }
                customDate != null -> {
                    licenseKey = licensegen.Licensegen.generateWithDate(machineCode, customDate!!)
                    expireDateForDisplay = customDate!!
                }
                selected.endsWith("天") -> {
                    val days = selected.dropLast(1).toIntOrNull() ?: throw Exception("无效天数")
                    licenseKey = licensegen.Licensegen.generate(machineCode, days.toLong())
                    val cal = java.util.Calendar.getInstance()
                    cal.add(java.util.Calendar.DAY_OF_YEAR, days)
                    expireDateForDisplay = dateFormat.format(cal.time)
                }
                else -> {
                    expireLayout.error = "请选择过期时间"
                    return
                }
            }

            // 显示结果
            resultText.text = licenseKey
            detailText.text = "机器码: $machineCode\n过期时间: $expireDateForDisplay"
            resultContainer.visibility = android.view.View.VISIBLE

        } catch (e: Exception) {
            Toast.makeText(this, "生成失败: ${e.message}", Toast.LENGTH_LONG).show()
        }
    }

    private fun copyToClipboard() {
        val text = resultText.text.toString()
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val clip = ClipData.newPlainText("授权码", text)
        clipboard.setPrimaryClip(clip)
        Toast.makeText(this, "已复制到剪贴板", Toast.LENGTH_SHORT).show()
    }
}