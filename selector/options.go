package selector

// selectOptions collects the per-call options of [Selector.Select].
type selectOptions struct {
	filters []NodeFilter
}

// SelectOption configures one Select call.
type SelectOption func(*selectOptions)

// WithNodeFilter appends node filters to the Select call. Every application
// of the option adds to the filters earlier applications contributed, and
// filters run in the order given.
func WithNodeFilter(fn ...NodeFilter) SelectOption {
	return func(opts *selectOptions) {
		opts.filters = append(opts.filters, fn...)
	}
}
