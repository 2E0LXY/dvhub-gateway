package uk.co.dvhub.android

import android.view.LayoutInflater
import android.view.ViewGroup
import androidx.recyclerview.widget.RecyclerView
import uk.co.dvhub.android.databinding.ItemActivityBinding

class ActivityAdapter : RecyclerView.Adapter<ActivityAdapter.Holder>() {
    private val items = mutableListOf<RadioActivity>()
    fun add(item: RadioActivity) {
        items.add(0, item)
        if (items.size > 100) items.removeAt(items.lastIndex)
        notifyDataSetChanged()
    }
    class Holder(val binding: ItemActivityBinding) : RecyclerView.ViewHolder(binding.root)
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) = Holder(ItemActivityBinding.inflate(LayoutInflater.from(parent.context), parent, false))
    override fun getItemCount() = items.size
    override fun onBindViewHolder(holder: Holder, position: Int) = with(holder.binding) {
        val item = items[position]
        identity.text = item.identity; nameLocation.text = item.nameLocation; route.text = item.route
        metrics.text = item.metrics; time.text = item.time
    }
}
