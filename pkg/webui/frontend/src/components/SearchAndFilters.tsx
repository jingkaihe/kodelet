import React, { useState } from 'react';
import { SearchFilters } from '../types';
import { debounce } from '../utils';

interface SearchAndFiltersProps {
  filters: SearchFilters;
  onFiltersChange: (filters: Partial<SearchFilters>) => void;
  onSearch: (searchTerm: string) => void;
  onClearFilters: () => void;
}

const SearchAndFilters: React.FC<SearchAndFiltersProps> = ({
  filters,
  onFiltersChange,
  onSearch,
  onClearFilters,
}) => {
  const [searchInput, setSearchInput] = useState(filters.searchTerm);

  // Debounced search function
  const debouncedSearch = debounce((term: string) => {
    onSearch(term);
  }, 300);

  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setSearchInput(value);
    debouncedSearch(value);
  };

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onSearch(searchInput);
  };

  const handleSortByChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    onFiltersChange({ sortBy: e.target.value as SearchFilters['sortBy'] });
  };

  const handleSortOrderChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    onFiltersChange({ sortOrder: e.target.value as SearchFilters['sortOrder'] });
  };

  const handleLimitChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    onFiltersChange({ limit: parseInt(e.target.value) });
  };

  return (
    <div className="card bg-base-200 shadow-xl mb-6">
      <div className="card-body">
        <h2 className="card-title mb-4">Search & Filter</h2>

        {/* Search Input */}
        <form onSubmit={handleSearchSubmit} className="form-control mb-4">
          <div className="input-group">
            <input
              type="text"
              placeholder="Search conversations..."
              className="input input-bordered flex-1"
              value={searchInput}
              onChange={handleSearchChange}
              aria-label="Search conversations"
            />
            <button 
              type="submit" 
              className="btn btn-primary"
              aria-label="Search"
            >
              <svg 
                xmlns="http://www.w3.org/2000/svg" 
                className="h-5 w-5" 
                fill="none" 
                viewBox="0 0 24 24" 
                stroke="currentColor"
                aria-hidden="true"
              >
                <path 
                  strokeLinecap="round" 
                  strokeLinejoin="round" 
                  strokeWidth="2" 
                  d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" 
                />
              </svg>
            </button>
          </div>
        </form>

        {/* Filter Options */}
        <div className="flex flex-wrap gap-4">
          <div className="form-control">
            <label className="label">
              <span className="label-text">Sort by</span>
            </label>
            <select 
              className="select select-bordered" 
              value={filters.sortBy}
              onChange={handleSortByChange}
              aria-label="Sort by"
            >
              <option value="updated">Last Updated</option>
              <option value="created">Created</option>
              <option value="messages">Message Count</option>
            </select>
          </div>

          <div className="form-control">
            <label className="label">
              <span className="label-text">Order</span>
            </label>
            <select 
              className="select select-bordered" 
              value={filters.sortOrder}
              onChange={handleSortOrderChange}
              aria-label="Sort order"
            >
              <option value="desc">Newest First</option>
              <option value="asc">Oldest First</option>
            </select>
          </div>

          <div className="form-control">
            <label className="label">
              <span className="label-text">Limit</span>
            </label>
            <select 
              className="select select-bordered" 
              value={filters.limit}
              onChange={handleLimitChange}
              aria-label="Items per page"
            >
              <option value="10">10</option>
              <option value="25">25</option>
              <option value="50">50</option>
              <option value="100">100</option>
            </select>
          </div>
        </div>

        {/* Clear Filters Button */}
        <div className="mt-4">
          <button 
            className="btn btn-outline btn-sm" 
            onClick={onClearFilters}
            aria-label="Clear all filters"
          >
            Clear Filters
          </button>
        </div>
      </div>
    </div>
  );
};

export default SearchAndFilters;