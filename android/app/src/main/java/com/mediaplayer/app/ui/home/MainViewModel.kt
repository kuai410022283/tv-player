package com.mediaplayer.app.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelGroup
import com.mediaplayer.app.data.repository.ChannelRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class MainViewModel : ViewModel() {

    private val repository = ChannelRepository()

    private val _groups = MutableStateFlow<List<ChannelGroup>>(emptyList())
    val groups: StateFlow<List<ChannelGroup>> = _groups.asStateFlow()

    private val _allChannels = MutableStateFlow<List<Channel>>(emptyList())
    val allChannels: StateFlow<List<Channel>> = _allChannels.asStateFlow()

    private val _displayChannels = MutableStateFlow<List<Channel>>(emptyList())
    val displayChannels: StateFlow<List<Channel>> = _displayChannels.asStateFlow()

    private val _isLoading = MutableStateFlow(false)
    val isLoading: StateFlow<Boolean> = _isLoading.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    fun loadData() {
        if (_isLoading.value) return
        _isLoading.value = true
        _error.value = null

        viewModelScope.launch {
            val realGroups = repository.getGroups().getOrElse { emptyList() }
            val allGroups = listOf(ChannelGroup(id = 0, name = "全部")) + realGroups
            _groups.value = allGroups

            val channelsResult = repository.getAllChannelsByGroups(realGroups)
            channelsResult.onSuccess { channels ->
                _allChannels.value = channels
                filterChannelsByGroup(allGroups.firstOrNull())
            }.onFailure { e ->
                _error.value = "加载频道失败: ${e.message}"
            }
            _isLoading.value = false
        }
    }

    fun selectGroup(group: ChannelGroup?) {
        filterChannelsByGroup(group)
    }

    private fun filterChannelsByGroup(group: ChannelGroup?) {
        if (group == null) {
            _displayChannels.value = emptyList()
            return
        }
        _displayChannels.value = if (group.id == 0L) {
            _allChannels.value
        } else {
            _allChannels.value.filter { it.groupId == group.id }
        }
    }

    fun searchChannels(query: String) {
        if (query.isEmpty()) return
        viewModelScope.launch {
            _isLoading.value = true
            repository.searchChannels(query).onSuccess { results ->
                _displayChannels.value = results
            }.onFailure { e ->
                _error.value = "搜索失败: ${e.message}"
            }
            _isLoading.value = false
        }
    }

    suspend fun getEPG(channelId: String) = repository.getEPG(channelId)
}
