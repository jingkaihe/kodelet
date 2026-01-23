import { useSearchParams } from 'react-router-dom';
import { useCallback, useMemo } from 'react';
import { SearchFilters } from '../types';

const defaultFilters: SearchFilters = {
  searchTerm: '',
  sortBy: 'updated',
  sortOrder: 'desc',
  limit: 25,
  offset: 0,
};

/**
 * Hook to manage search filters persisted in URL search parameters
 */
export const useUrlFilters = () => {
  const [searchParams, setSearchParams] = useSearchParams();


  // Parse filters from URL with stable dependencies
  const filters = useMemo((): SearchFilters => {
    const search = searchParams.get('search') || defaultFilters.searchTerm;
    const sortBy = searchParams.get('sortBy') as SearchFilters['sortBy'] || defaultFilters.sortBy;
    const sortOrder = searchParams.get('sortOrder') as SearchFilters['sortOrder'] || defaultFilters.sortOrder;
    const limit = parseInt(searchParams.get('limit') || '25', 10) || defaultFilters.limit;
    const offset = parseInt(searchParams.get('offset') || '0', 10) || defaultFilters.offset;

    // Validate sortBy and sortOrder values
    const validSortBy = ['updated', 'created', 'messages'].includes(sortBy) ? sortBy : defaultFilters.sortBy;
    const validSortOrder = ['asc', 'desc'].includes(sortOrder) ? sortOrder : defaultFilters.sortOrder;
    const validLimit = [10, 25, 50, 100].includes(limit) ? limit : defaultFilters.limit;

    return {
      searchTerm: search,
      sortBy: validSortBy as SearchFilters['sortBy'],
      sortOrder: validSortOrder as SearchFilters['sortOrder'],
      limit: validLimit,
      offset: Math.max(0, offset),
    };
  }, [searchParams]); // Include searchParams dependency

  // Update filters in URL with completely stable dependencies
  const updateFilters = useCallback((newFilters: Partial<SearchFilters>) => {
    setSearchParams((prevParams) => {
      // Get current filters from the previous params
      const currentSearch = prevParams.get('search') || defaultFilters.searchTerm;
      const currentSortBy = (prevParams.get('sortBy') as SearchFilters['sortBy']) || defaultFilters.sortBy;
      const currentSortOrder = (prevParams.get('sortOrder') as SearchFilters['sortOrder']) || defaultFilters.sortOrder;
      const currentLimit = parseInt(prevParams.get('limit') || '25', 10) || defaultFilters.limit;
      const currentOffset = parseInt(prevParams.get('offset') || '0', 10) || defaultFilters.offset;

      const currentFilters = {
        searchTerm: currentSearch,
        sortBy: currentSortBy,
        sortOrder: currentSortOrder,
        limit: currentLimit,
        offset: currentOffset,
      };

      const updatedFilters = { ...currentFilters, ...newFilters };

      // Reset offset when other filters change (except when explicitly setting offset)
      if (newFilters.offset === undefined &&
          (newFilters.searchTerm !== undefined ||
           newFilters.sortBy !== undefined ||
           newFilters.sortOrder !== undefined ||
           newFilters.limit !== undefined)) {
        updatedFilters.offset = 0;
      }

      const newSearchParams = new URLSearchParams();

      // Only add non-default values to keep URLs clean
      if (updatedFilters.searchTerm && updatedFilters.searchTerm !== defaultFilters.searchTerm) {
        newSearchParams.set('search', updatedFilters.searchTerm);
      }
      if (updatedFilters.sortBy !== defaultFilters.sortBy) {
        newSearchParams.set('sortBy', updatedFilters.sortBy);
      }
      if (updatedFilters.sortOrder !== defaultFilters.sortOrder) {
        newSearchParams.set('sortOrder', updatedFilters.sortOrder);
      }
      if (updatedFilters.limit !== defaultFilters.limit) {
        newSearchParams.set('limit', updatedFilters.limit.toString());
      }
      if (updatedFilters.offset && updatedFilters.offset !== defaultFilters.offset) {
        newSearchParams.set('offset', updatedFilters.offset.toString());
      }

      return newSearchParams;
    }, { replace: true });
  }, [setSearchParams]); // Include setSearchParams dependency

  // Helper function to clear all filters with stable dependencies
  const clearFilters = useCallback(() => {
    setSearchParams(new URLSearchParams(), { replace: true });
  }, [setSearchParams]); // Include setSearchParams dependency

  // Helper function to go to specific page with stable dependencies
  const goToPage = useCallback((page: number) => {
    setSearchParams((prevParams) => {
      const currentLimit = parseInt(prevParams.get('limit') || '25', 10) || defaultFilters.limit;
      const newOffset = (page - 1) * currentLimit;

      const newSearchParams = new URLSearchParams(prevParams);
      if (newOffset > 0) {
        newSearchParams.set('offset', newOffset.toString());
      } else {
        newSearchParams.delete('offset');
      }

      return newSearchParams;
    }, { replace: true });
  }, [setSearchParams]); // Include setSearchParams dependency

  // Calculate current page from stable filters
  const currentPage = useMemo(() => {
    return Math.floor(filters.offset / filters.limit) + 1;
  }, [filters.offset, filters.limit]);

  return {
    filters,
    updateFilters,
    clearFilters,
    goToPage,
    currentPage,
  };
};